package engine

// The routing table (config.Routing), decided the way 3x-ui's Xray does,
// with NeoExit's own steps around it:
//
//  1. an address inside the server's networks: refused, or direct when
//     allowed (3x-ui's geoip:private);
//  2. a proxy set to one exit: that exit;
//  3. the AI services, each through the exit the engine picked for it,
//     and other AI sites (HMray "ai" lists) through the AI services' exit,
//     so AI always opens whatever the rules below say;
//  4. the table, top to bottom; a name no rule matched is then looked up
//     and the table checked again with its address (IPIfNonMatch);
//  5. the rest: the exit for other traffic, or direct.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/dns"
	"neoexit/internal/exit"
	"neoexit/internal/netguard"
	"neoexit/internal/route"
	"neoexit/internal/routing"
)

// Table is the routing table, compiled.
type Table struct {
	rules  []rule
	needIP bool // some rule has IPs: look names up when nothing matched
	sniff  bool // some rule names a protocol: read every connection's first bytes
}

type rule struct {
	id        string
	dom, ip   *routing.Matcher
	hasDom    bool
	hasIP     bool
	ports     [][2]int
	network   string
	protocols []string
	inbounds  []string
	target    string
}

// CompileTable checks the rules; the error says which is wrong.
func CompileTable(rules []config.Rule) (*Table, error) {
	t := &Table{}
	for i, x := range rules {
		if x.Disabled {
			continue
		}
		r, err := compileRule(x)
		if err != nil {
			return nil, fmt.Errorf("قانون %d: %v", i+1, err)
		}
		t.add(r)
	}
	return t, nil
}

func (t *Table) add(r rule) {
	t.rules = append(t.rules, r)
	t.needIP = t.needIP || r.hasIP
	t.sniff = t.sniff || len(r.protocols) > 0
}

func compileRule(x config.Rule) (rule, error) {
	dm, err := routing.Build(x.Domains, nil)
	if err != nil {
		return rule{}, err
	}
	im, err := routing.Build(nil, x.IPs)
	if err != nil {
		return rule{}, err
	}
	ports, err := parsePorts(x.Ports)
	if err != nil {
		return rule{}, err
	}
	return rule{id: x.ID, dom: dm, ip: im, hasDom: len(x.Domains) > 0, hasIP: len(x.IPs) > 0,
		ports: ports, network: x.Network, protocols: x.Protocols, inbounds: x.Inbounds, target: x.Target}, nil
}

// SetRouting puts the saved table in use. A rule that no longer compiles
// (a list file was removed) is left out and logged.
func (e *Engine) SetRouting() {
	cfg := e.store.Get()
	t, err := CompileTable(cfg.Routing)
	if err != nil {
		t = &Table{}
		for i, x := range cfg.Routing {
			if x.Disabled {
				continue
			}
			r, err := compileRule(x)
			if err != nil {
				log.Printf("routing: rule %d left out: %v", i+1, err)
				continue
			}
			t.add(r)
		}
	}
	e.table.Store(t)
}

// Sniff says whether proxies should read the first bytes of every
// connection (a rule names a protocol), not only of those asked by IP.
func (e *Engine) Sniff() bool {
	t := e.table.Load()
	return t != nil && t.sniff
}

var (
	aiOnce sync.Once
	aiSite *routing.Matcher
)

// aiSites are AI sites beyond the services (HMray's "ai" lists).
func aiSites() *routing.Matcher {
	aiOnce.Do(func() {
		m, err := routing.Build([]string{"ext:hmray.dat:ai", "ext:hmray.dat:openai"}, nil)
		if err != nil {
			log.Printf("AI lists: %v", err)
		}
		aiSite = m
	})
	return aiSite
}

// Decide picks where a request goes with the table in use.
func (e *Engine) Decide(req route.Request) route.Decision {
	t := e.table.Load()
	if t == nil {
		t = &Table{}
	}
	return e.DecideWith(t, req)
}

// DecideWith decides with a given table (the panel's tester tries one
// before it is saved).
func (e *Engine) DecideWith(t *Table, req route.Request) route.Decision {
	cfg := e.store.Get()
	host := strings.ToLower(strings.TrimSuffix(strings.Trim(req.Host, "[]"), "."))
	ip := net.ParseIP(host)
	name := host
	if ip != nil {
		name = strings.ToLower(strings.TrimSuffix(req.Sniffed, "."))
	}
	// 1. the server's own networks
	if ip != nil && netguard.IsPrivate(ip) {
		if netguard.Blocked(ip) {
			return route.Decision{Target: config.TargetBlock, Rule: "private"}
		}
		return route.Decision{Target: config.TargetDirect, Rule: "private"}
	}
	// 2. a proxy on one exit
	if x := proxyExit(cfg, req.Proxy); x != "" {
		return route.Decision{Target: x, Rule: "exit"}
	}
	// 3. AI
	if name != "" {
		e.mu.Lock()
		x, svc, ok := e.serviceExitLocked(cfg, name)
		if !ok {
			if m := aiSites(); m != nil && m.MatchHost(name) {
				x, ok, svc = e.aiExitLocked(cfg), true, "ai"
			}
		}
		e.mu.Unlock()
		if ok && x != "" {
			return route.Decision{Target: x, Rule: "service:" + svc}
		}
	}
	// 4. the table; then, for a name, again with its address
	if d, ok := t.match(req, name, ip); ok {
		return d
	}
	if ip == nil && t.needIP && name != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ips, _, err := dns.Lookup(ctx, name)
		cancel()
		if err == nil && len(ips) > 0 {
			if netguard.Blocked(ips[0]) {
				return route.Decision{Target: config.TargetBlock, Rule: "private"}
			}
			if d, ok := t.match(req, name, ips[0]); ok {
				return d
			}
		}
	}
	// 5. the rest
	if cfg.Other != "" {
		return route.Decision{Target: cfg.Other, Rule: "default"}
	}
	return route.Decision{Target: config.TargetDirect, Rule: "default"}
}

func proxyExit(cfg config.Config, id string) string {
	if id == "" || id == "main" {
		return cfg.Proxy.Exit
	}
	if x := cfg.ExtraProxy(id); x != nil {
		return x.Exit
	}
	return ""
}

// aiExitLocked is the exit for AI sites that aren't a service: where the
// first service that has a working exit goes, else the first exit up.
func (e *Engine) aiExitLocked(cfg config.Config) string {
	for _, s := range cfg.Services {
		if !s.Enabled {
			continue
		}
		if st := e.services[s.ID]; st != nil && e.dialers[st.Exit] != nil {
			return st.Exit
		}
	}
	for _, x := range cfg.Exits {
		if e.dialers[x.ID] != nil {
			return x.ID
		}
	}
	return ""
}

// match finds the first rule that holds. ip is the address asked for or
// looked up (nil: only a name is known).
func (t *Table) match(req route.Request, name string, ip net.IP) (route.Decision, bool) {
	for _, r := range t.rules {
		if r.holds(req, name, ip) {
			return route.Decision{Target: r.target, Rule: "rule:" + r.id}, true
		}
	}
	return route.Decision{}, false
}

func (r rule) holds(req route.Request, name string, ip net.IP) bool {
	if len(r.inbounds) > 0 {
		in := req.Proxy
		if in == "" {
			in = "main"
		}
		if !slices.Contains(r.inbounds, in) {
			return false
		}
	}
	if r.network != "" && r.network != req.Network {
		return false
	}
	if len(r.protocols) > 0 && !slices.Contains(r.protocols, req.Protocol) {
		return false
	}
	if len(r.ports) > 0 {
		in := false
		for _, p := range r.ports {
			if req.Port >= p[0] && req.Port <= p[1] {
				in = true
				break
			}
		}
		if !in {
			return false
		}
	}
	if r.hasDom && (name == "" || !r.dom.MatchHost(name)) {
		return false
	}
	if r.hasIP && (ip == nil || !r.ip.MatchIP(ip)) {
		return false
	}
	return true
}

// Route decides and turns the target into a dialer: nil is the server's
// route; an exit that is down refuses rather than letting traffic out
// another way.
func (e *Engine) Route(req route.Request) (exit.Dialer, route.Decision) {
	d := e.Decide(req)
	return e.target(d.Target), d
}

func (e *Engine) target(t string) exit.Dialer {
	switch t {
	case config.TargetDirect:
		return nil
	case config.TargetBlock:
		return blockDialer{}
	}
	if d := e.Dialer(t); d != nil {
		return d
	}
	return downDialer{}
}

type blockDialer struct{}

func (blockDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("blocked by the routing")
}
func (blockDialer) Close() error { return nil }

func parsePorts(s string) ([][2]int, error) {
	var out [][2]int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '،' }) {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return nil, fmt.Errorf("پورت «%s» درست نیست", part)
		}
		out = append(out, [2]int{a, b})
	}
	return out, nil
}

// --- stats ------------------------------------------------------------------

// Counter is what went through one rule (or to one target).
type Counter struct {
	Conns int64 `json:"conns"`
	Bytes int64 `json:"bytes"`
}

// Count adds a finished connection (or UDP destination) to the stats.
func (e *Engine) Count(d route.Decision, bytes int64) {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	for _, k := range []string{d.Rule, "target:" + d.Target} {
		c := e.stats[k]
		if c == nil {
			c = &Counter{}
			e.stats[k] = c
		}
		c.Conns++
		c.Bytes += bytes
	}
}

// Stats is a copy of the counts since start, by rule ("rule:<id>",
// "service:<id>", "private", "exit", "default") and "target:<id>".
func (e *Engine) Stats() map[string]Counter {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	out := map[string]Counter{}
	for k, c := range e.stats {
		out[k] = *c
	}
	return out
}
