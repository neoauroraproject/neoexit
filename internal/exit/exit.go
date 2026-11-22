// Package exit turns each configured exit into something that dials:
// the server's own IPv4 or IPv6, a WARP account, or an upstream proxy.
package exit

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"

	"neoexit/internal/config"
	"neoexit/internal/dns"
	"neoexit/internal/opera"
	"neoexit/internal/psiphon"
	"neoexit/internal/warp"
)

type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	Close() error
}

// UDPDialer is an exit that can carry UDP too (the server's own
// addresses and WARP); UDP sent to any other exit is dropped.
type UDPDialer interface {
	DialUDP(ctx context.Context, addr string) (net.Conn, error)
}

// Slow kinds need the network (or a download) to start; the engine builds
// them in the background.
func Slow(kind string) bool {
	return kind == config.Opera || kind == config.Psiphon || kind == config.SSH
}

// Options are what some kinds need besides their settings.
type Options struct {
	DataDir   string           // where Psiphon keeps its program and data
	OnHostKey func(key string) // an SSH exit learned its server's key
}

// New builds the dialer for e. A WARP exit without an account must have
// one registered first (the engine does that).
func New(ctx context.Context, e config.Exit, o Options) (Dialer, error) {
	switch e.Kind {
	case config.Opera:
		return opera.Start(ctx, e.Region)
	case config.Psiphon:
		return psiphon.Start(ctx, o.DataDir, e.ID, e.Region)
	case config.SSH:
		s := &sshExit{addr: e.Address, user: e.User, pass: e.Pass, key: e.Key, hostKey: e.HostKey, onHostKey: o.OnHostKey}
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if _, err := s.connect(cctx); err != nil {
			return nil, fmt.Errorf("SSH: %w", err)
		}
		return s, nil
	case config.Direct4:
		return direct{"4"}, nil
	case config.Direct6:
		return direct{"6"}, nil
	case config.WARP:
		if e.Account == nil {
			return nil, errors.New("no WARP account yet")
		}
		return warp.Start(e.Account)
	case config.SOCKS5:
		var auth *proxy.Auth
		if e.User != "" {
			auth = &proxy.Auth{User: e.User, Password: e.Pass}
		}
		d, err := proxy.SOCKS5("tcp", e.Address, auth, &net.Dialer{Timeout: 10 * time.Second})
		if err != nil {
			return nil, err
		}
		return socks{d.(proxy.ContextDialer)}, nil
	case config.HTTP:
		return httpProxy{addr: e.Address, user: e.User, pass: e.Pass}, nil
	}
	return nil, fmt.Errorf("unknown exit kind %q", e.Kind)
}

// direct dials from the server itself, over one address family only.
type direct struct{ family string }

func (d direct) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return dns.Dial(ctx, "tcp"+d.family, addr)
}

func (d direct) DialUDP(ctx context.Context, addr string) (net.Conn, error) {
	return dns.Dial(ctx, "udp"+d.family, addr)
}

func (direct) Close() error { return nil }

type socks struct{ d proxy.ContextDialer }

func (s socks) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return s.d.DialContext(ctx, "tcp", addr)
}

func (socks) Close() error { return nil }

type httpProxy struct{ addr, user, pass string }

func (h httpProxy) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var nd net.Dialer
	c, err := nd.DialContext(ctx, "tcp", h.addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	} else {
		c.SetDeadline(time.Now().Add(15 * time.Second))
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if h.user != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(h.user+":"+h.pass)) + "\r\n"
	}
	if _, err := c.Write([]byte(req + "\r\n")); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.StatusCode != 200 {
		c.Close()
		return nil, fmt.Errorf("proxy refused: %s", resp.Status)
	}
	c.SetDeadline(time.Time{})
	if br.Buffered() > 0 {
		return &buffered{Conn: c, r: br}, nil
	}
	return c, nil
}

func (httpProxy) Close() error { return nil }

type buffered struct {
	net.Conn
	r *bufio.Reader
}

func (b *buffered) Read(p []byte) (int, error) { return b.r.Read(p) }
