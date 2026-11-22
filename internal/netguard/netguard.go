// Package netguard keeps proxy clients out of the server's own networks:
// loopback, private ranges, link-local and the like (what Xray's
// "geoip:private" blocks). 3x-ui blocks them with a routing rule; once its
// traffic is routed by NeoExit instead, NeoExit has to.
package netguard

import (
	"errors"
	"net"
	"sync"
	"syscall"
)

var private = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		_, n, _ := net.ParseCIDR(s)
		out = append(out, n)
	}
	return out
}()

var (
	mu     sync.RWMutex
	allow  []*net.IPNet
	direct bool // the server's networks may be reached (3x-ui's private → direct)
)

// SetDirect lets proxies reach the server's own networks.
func SetDirect(on bool) {
	mu.Lock()
	direct = on
	mu.Unlock()
}

// SetAllow sets the addresses that may be reached anyway (CIDRs or IPs;
// bad ones are skipped, the settings check them).
func SetAllow(list []string) {
	var out []*net.IPNet
	for _, s := range list {
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		} else if ip := net.ParseIP(s); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	mu.Lock()
	allow = out
	mu.Unlock()
}

// IsPrivate says whether ip is inside the server's own networks (what
// Xray calls geoip:private).
func IsPrivate(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range private {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Blocked says whether a proxy may not reach ip.
func Blocked(ip net.IP) bool {
	if !IsPrivate(ip) {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	mu.RLock()
	defer mu.RUnlock()
	if direct {
		return false
	}
	for _, n := range allow {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var ErrBlocked = errors.New("address inside the server's own networks")

// Control is a net.Dialer Control: it refuses blocked addresses after a
// name was resolved, so a domain can't lead to them either.
func Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	if Blocked(net.ParseIP(host)) {
		return ErrBlocked
	}
	return nil
}
