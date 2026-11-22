// Package dns resolves names for the traffic NeoExit sends out itself
// (the server's route and its own IPv4/IPv6 exits), with split DNS:
// Iranian names through Iranian DNS servers, so they get the addresses
// Iranian sites give inside Iran, and every other name through the
// server's DNS (or chosen servers). Exits like WARP resolve on their own
// side and don't use this.
package dns

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/netguard"
	"neoexit/internal/routing"
)

type state struct {
	ir      *routing.Matcher // nil: off
	irRes   []*net.Resolver  // one per Iranian server, asked together
	global  *net.Resolver
	irList  []string
	globals []string
}

var (
	mu  sync.RWMutex
	cur = &state{global: net.DefaultResolver}

	cacheMu sync.Mutex
	cache   = map[string]entry{}
)

type entry struct {
	ips []net.IP
	via string
	end time.Time
}

// resolver asks one of servers (ip or ip:port), a random one first.
func resolver(servers []string) *net.Resolver {
	if len(servers) == 0 {
		return net.DefaultResolver
	}
	addrs := make([]string, len(servers))
	for i, s := range servers {
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53")
		}
		addrs[i] = s
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		start := rand.IntN(len(addrs))
		var err error
		for i := range addrs {
			var c net.Conn
			if c, err = d.DialContext(ctx, network, addrs[(start+i)%len(addrs)]); err == nil {
				return c, nil
			}
		}
		return nil, err
	}}
}

// Set applies the settings; a bad Iranian domain list turns the Iranian
// DNS off and says why.
func Set(c config.DNS) error {
	st := &state{global: resolver(c.Servers), globals: c.Servers}
	var err error
	if !c.IROff {
		st.irList = c.IRServers
		if len(st.irList) == 0 {
			st.irList = config.DefaultIRServers
		}
		doms := c.IRDomains
		if len(doms) == 0 {
			doms = config.DefaultIRDomains
		}
		st.ir, err = routing.Build(doms, nil)
		if err == nil {
			for _, s := range st.irList {
				st.irRes = append(st.irRes, resolver([]string{s}))
			}
		} else {
			st.ir = nil
		}
	}
	mu.Lock()
	cur = st
	mu.Unlock()
	cacheMu.Lock()
	cache = map[string]entry{}
	cacheMu.Unlock()
	return err
}

// IsIranian says whether host is resolved through the Iranian servers.
func IsIranian(host string) bool {
	mu.RLock()
	st := cur
	mu.RUnlock()
	return st.ir != nil && st.ir.MatchHost(strings.ToLower(strings.TrimSuffix(host, ".")))
}

// Lookup resolves host; via says which DNS answered ("ir" or "global").
func Lookup(ctx context.Context, host string) (ips []net.IP, via string, err error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, "", nil
	}
	cacheMu.Lock()
	if e, ok := cache[host]; ok && time.Now().Before(e.end) {
		cacheMu.Unlock()
		return e.ips, e.via, nil
	}
	cacheMu.Unlock()
	mu.RLock()
	st := cur
	mu.RUnlock()

	via = "global"
	if st.ir != nil && st.ir.MatchHost(host) {
		if ips = race(ctx, st.irRes, host); ips != nil {
			via = "ir"
		} // else the Iranian servers didn't answer: the usual DNS
	}
	if ips == nil {
		gctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err = st.global.LookupIP(gctx, "ip", host)
		cancel()
		if err != nil {
			return nil, via, err
		}
	}
	cacheMu.Lock()
	if len(cache) > 20000 {
		cache = map[string]entry{}
	}
	cache[host] = entry{ips, via, time.Now().Add(2 * time.Minute)}
	cacheMu.Unlock()
	return ips, via, nil
}

// race asks every resolver at once and takes the first answer, so one
// server that doesn't answer (some don't, from outside Iran) costs
// nothing.
func race(ctx context.Context, rs []*net.Resolver, host string) []net.IP {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ch := make(chan []net.IP, len(rs))
	for _, r := range rs {
		go func() {
			ips, err := r.LookupIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				ips = nil
			}
			ch <- ips
		}()
	}
	for range rs {
		if ips := <-ch; ips != nil {
			return ips
		}
	}
	return nil
}

// Dial connects from the server itself (network "tcp", "tcp4", "udp6"...),
// resolving through the split DNS and never into the server's own
// networks unless allowed.
func Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, _, err := Lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	want4, want6 := !strings.HasSuffix(network, "6"), !strings.HasSuffix(network, "4")
	var v4, v6 []net.IP
	for _, ip := range ips {
		if ip.To4() != nil {
			if want4 {
				v4 = append(v4, ip)
			}
		} else if want6 {
			v6 = append(v6, ip)
		}
	}
	try := append(v4, v6...) // IPv4 first, as most servers route it best
	if len(try) == 0 {
		return nil, fmt.Errorf("%s has no %s address", host, map[bool]string{true: "IPv4", false: "IPv6"}[strings.HasSuffix(network, "4")])
	}
	d := net.Dialer{Timeout: 10 * time.Second, Control: netguard.Control}
	var last error
	for i, ip := range try {
		if i == 3 {
			break
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
		if errors.Is(err, netguard.ErrBlocked) || ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// Test resolves host now (not from the cache) and says through which
// DNS and how long it took.
func Test(ctx context.Context, host string) (ips []net.IP, via string, ms int64, err error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	cacheMu.Lock()
	delete(cache, host)
	cacheMu.Unlock()
	start := time.Now()
	ips, via, err = Lookup(ctx, host)
	return ips, via, time.Since(start).Milliseconds(), err
}
