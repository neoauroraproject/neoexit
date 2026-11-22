package web

// The routing table, the server's own networks and the split DNS, as the
// panel edits them.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/dns"
	"neoexit/internal/engine"
	"neoexit/internal/netguard"
	"neoexit/internal/route"
)

// ApplyNet puts the saved private-network and DNS settings in use.
func ApplyNet(cfg config.Config) error {
	netguard.SetAllow(cfg.AllowPrivate)
	netguard.SetDirect(cfg.PrivateDirect)
	return dns.Set(cfg.DNS)
}

type routingInput struct {
	Rules         []config.Rule `json:"rules"`
	Other         *string       `json:"other"`
	PrivateDirect *bool         `json:"private_direct"`
	AllowPrivate  []string      `json:"allow_private"`
}

// cleanRules trims what was typed (comma- or line-separated, as in
// 3x-ui) and gives new rules an ID.
func cleanRules(c *config.Config, in []config.Rule) []config.Rule {
	out := make([]config.Rule, 0, len(in))
	for _, x := range in {
		x.Name = strings.TrimSpace(x.Name)
		x.Domains = trimList(x.Domains)
		x.IPs = trimList(x.IPs)
		x.Ports = strings.TrimSpace(x.Ports)
		x.Protocols = trimList(x.Protocols)
		x.Inbounds = trimList(x.Inbounds)
		if x.ID == "" {
			c.Routing = append(c.Routing, config.Rule{ID: c.NewRuleID()})
			x.ID = c.Routing[len(c.Routing)-1].ID
		}
		out = append(out, x)
	}
	return out
}

// PUT api/routing: the whole table (in order), the exit for the rest and
// the server's own networks.
func (a *App) putRouting(w http.ResponseWriter, r *http.Request) {
	var in routingInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	err := a.Store.Update(func(c *config.Config) error {
		rules := cleanRules(c, in.Rules)
		if _, err := engine.CompileTable(rules); err != nil {
			return err
		}
		c.Routing = rules
		if in.Other != nil {
			c.Other = *in.Other
		}
		if in.PrivateDirect != nil {
			c.PrivateDirect = *in.PrivateDirect
		}
		if in.AllowPrivate != nil {
			c.AllowPrivate = trimList(in.AllowPrivate)
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ApplyNet(a.Store.Get())
	a.Engine.SetRouting()
	writeJSON(w, map[string]bool{"ok": true})
}

// POST api/routing/test: where would this go? With "rules", the table
// being edited is tried before it is saved. Nothing is dialed.
func (a *App) testRouting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host     string         `json:"host"`
		Port     int            `json:"port"`
		Network  string         `json:"network"`
		Protocol string         `json:"protocol"`
		Proxy    string         `json:"proxy"`
		Rules    *[]config.Rule `json:"rules"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.Host = strings.TrimSpace(in.Host)
	for _, p := range []struct {
		pre  string
		port int
	}{{"https://", 443}, {"http://", 80}} {
		if u, ok := strings.CutPrefix(in.Host, p.pre); ok {
			in.Host = u
			if in.Port == 0 {
				in.Port = p.port
			}
		}
	}
	in.Host, _, _ = strings.Cut(in.Host, "/")
	if h, p, err := net.SplitHostPort(in.Host); err == nil {
		in.Host = h
		if in.Port == 0 {
			in.Port = atoi(p)
		}
	}
	if in.Host == "" {
		writeErr(w, http.StatusBadRequest, "یک دامنه یا IP بنویسید")
		return
	}
	if in.Port == 0 {
		in.Port = 443
	}
	if in.Network != "udp" {
		in.Network = "tcp"
	}
	if in.Protocol == "" && in.Network == "tcp" && in.Port == 443 {
		in.Protocol = "tls"
	}
	req := route.Request{Host: in.Host, Port: in.Port, Network: in.Network, Protocol: in.Protocol, Proxy: in.Proxy}
	var d route.Decision
	if in.Rules != nil {
		cfg := a.Store.Get()
		t, err := engine.CompileTable(cleanRules(&cfg, *in.Rules))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		d = a.Engine.DecideWith(t, req)
	} else {
		d = a.Engine.Decide(req)
	}
	out := map[string]any{"decision": d}
	switch d.Target {
	case config.TargetBlock:
	case config.TargetDirect:
		out["dns"] = dnsVia(in.Host)
	default:
		out["up"] = a.Engine.Dialer(d.Target) != nil
		cfg := a.Store.Get()
		if x := cfg.Exit(d.Target); x != nil && (x.Kind == config.Direct4 || x.Kind == config.Direct6) {
			out["dns"] = dnsVia(in.Host)
		}
	}
	writeJSON(w, out)
}

func dnsVia(host string) string {
	if net.ParseIP(host) != nil {
		return ""
	}
	if dns.IsIranian(host) {
		return "ir"
	}
	return "global"
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// PUT api/dns
func (a *App) putDNS(w http.ResponseWriter, r *http.Request) {
	var in config.DNS
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.IRServers, in.IRDomains, in.Servers = trimList(in.IRServers), trimList(in.IRDomains), trimList(in.Servers)
	old := a.Store.Get().DNS
	err := a.Store.Update(func(c *config.Config) error { c.DNS = in; return nil })
	if err == nil {
		if err = dns.Set(in); err != nil {
			a.Store.Update(func(c *config.Config) error { c.DNS = old; return nil })
			dns.Set(old)
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// POST api/dns/test: resolve a name now, through the DNS it would use.
func (a *App) testDNS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string `json:"host"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Host) == "" {
		writeErr(w, http.StatusBadRequest, "یک دامنه بنویسید")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ips, via, ms, err := dns.Test(ctx, in.Host)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "پیدا نشد: "+err.Error())
		return
	}
	var list []string
	for _, ip := range ips {
		list = append(list, ip.String())
	}
	writeJSON(w, map[string]any{"ips": list, "via": via, "ms": ms})
}
