// Package proxy is the local SOCKS5 and HTTP proxy (one port; the first
// byte tells them apart). Each connection is routed by the port's Router
// (the engine's Plan): by host name, port, network and, when the client
// asked for an IP, the site name in its first bytes.
package proxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"neoexit/internal/dns"
	"neoexit/internal/exit"
	"neoexit/internal/route"
)

// Router picks the dialer for a request; nil means the server's own route.
type Router func(route.Request) (exit.Dialer, route.Decision)

type Server struct {
	// ID is this proxy ("main" or an extra proxy's), for rules that name
	// it, like Xray's inboundTag.
	ID         string
	Route      Router
	User, Pass string
	// Sniff: read the first bytes of every connection (a rule names a
	// protocol), not only of those asked for by IP.
	Sniff func() bool
	// Count is told about each finished connection (bytes both ways).
	Count func(route.Decision, int64)

	Active atomic.Int64 // open connections
	Total  atomic.Int64

	mu sync.Mutex
	ln net.Listener
}

func (s *Server) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			go s.serve(c)
		}
	}()
	return nil
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		s.ln.Close()
		s.ln = nil
	}
}

func (s *Server) serve(c net.Conn) {
	s.Active.Add(1)
	s.Total.Add(1)
	defer s.Active.Add(-1)
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 5 {
		s.socks(c, br)
	} else {
		s.http(c, br)
	}
}

func (s *Server) decide(req route.Request) (exit.Dialer, route.Decision) {
	if s.Route == nil {
		return nil, route.Decision{Target: "direct", Rule: "default"}
	}
	return s.Route(req)
}

func (s *Server) dial(ctx context.Context, req route.Request) (net.Conn, route.Decision, error) {
	d, dec := s.decide(req)
	addr := net.JoinHostPort(strings.Trim(req.Host, "[]"), strconv.Itoa(req.Port))
	if d == nil {
		// The server's own route: never into its own networks, even by a
		// name that points there.
		c, err := dns.Dial(ctx, "tcp", addr)
		return c, dec, err
	}
	c, err := d.DialContext(ctx, "tcp", addr)
	return c, dec, err
}

func (s *Server) count(d route.Decision, n int64) {
	if s.Count != nil {
		s.Count(d, n)
	}
}

// connect routes and dials host:port for a SOCKS CONNECT or an HTTP
// CONNECT, tells the client through reply, and pipes. When it has to see
// the client's first bytes (it asked for an IP, or BitTorrent is looked
// for), it says yes first: a failed dial then just closes.
func (s *Server) connect(c net.Conn, br *bufio.Reader, host, port string, reply func(ok bool)) {
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		reply(false)
		return
	}
	req := route.Request{Host: host, Port: p, Network: "tcp", Proxy: s.ID}
	early := (s.Sniff != nil && s.Sniff()) || net.ParseIP(strings.Trim(host, "[]")) != nil
	if early {
		reply(true)
		c.SetDeadline(time.Time{})
		b := peek(c, br)
		req.Sniffed, req.Protocol = sniffName(b), sniffProtocol(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	up, dec, err := s.dial(ctx, req)
	cancel()
	if err != nil {
		if !early {
			reply(false)
		}
		s.count(dec, 0)
		return
	}
	defer up.Close()
	if !early {
		reply(true)
	}
	c.SetDeadline(time.Time{})
	s.count(dec, pipe(c, br, up))
}

func (s *Server) auth(user, pass string) bool {
	if s.User == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(user), []byte(s.User)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.Pass)) == 1
}

func (s *Server) socks(c net.Conn, br *bufio.Reader) {
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	want := byte(0)
	if s.User != "" {
		want = 2
	}
	found := false
	for _, m := range methods {
		if m == want {
			found = true
		}
	}
	if !found {
		c.Write([]byte{5, 0xff})
		return
	}
	c.Write([]byte{5, want})
	if want == 2 {
		v := make([]byte, 2)
		if _, err := io.ReadFull(br, v); err != nil {
			return
		}
		u := make([]byte, v[1])
		io.ReadFull(br, u)
		pl := make([]byte, 1)
		io.ReadFull(br, pl)
		p := make([]byte, pl[0])
		io.ReadFull(br, p)
		if !s.auth(string(u), string(p)) {
			c.Write([]byte{1, 1})
			return
		}
		c.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	if req[1] != 1 && req[1] != 3 { // CONNECT and UDP ASSOCIATE
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 4:
		b := make([]byte, 16)
		io.ReadFull(br, b)
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		io.ReadFull(br, l)
		b := make([]byte, l[0])
		io.ReadFull(br, b)
		host = string(b)
	default:
		c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	if req[1] == 3 {
		s.associate(c, br)
		return
	}
	port := strconv.Itoa(int(binary.BigEndian.Uint16(pb)))
	s.connect(c, br, host, port, func(ok bool) {
		if ok {
			c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		} else {
			c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		}
	})
}

func (s *Server) http(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if s.User != "" {
		u, p, ok := proxyAuth(req.Header.Get("Proxy-Authorization"))
		if !ok || !s.auth(u, p) {
			io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"neoexit\"\r\nContent-Length: 0\r\n\r\n")
			return
		}
	}
	if req.Method == http.MethodConnect {
		host, port, err := net.SplitHostPort(req.Host)
		if err != nil {
			host, port = req.Host, "443"
		}
		s.connect(c, br, host, port, func(ok bool) {
			if ok {
				io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
			} else {
				io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			}
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Plain HTTP with an absolute URL.
	if req.URL.Host == "" {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	host, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		host, port = req.URL.Host, "80"
	}
	pn, _ := strconv.Atoi(port)
	up, dec, err := s.dial(ctx, route.Request{Host: host, Port: pn, Network: "tcp", Protocol: "http", Proxy: s.ID})
	if err != nil {
		s.count(dec, 0)
		io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.RequestURI = ""
	if err := req.Write(up); err != nil {
		return
	}
	c.SetDeadline(time.Time{})
	s.count(dec, pipe(c, br, up))
}

func proxyAuth(h string) (string, string, bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(h, prefix) {
		return "", "", false
	}
	b, err := base64.StdEncoding.DecodeString(h[len(prefix):])
	if err != nil {
		return "", "", false
	}
	u, p, ok := strings.Cut(string(b), ":")
	return u, p, ok
}

// pipe copies both ways until one side ends, and returns the bytes moved.
func pipe(c net.Conn, br *bufio.Reader, up net.Conn) int64 {
	var total atomic.Int64
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(up, br)
		total.Add(n)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(c, up)
		total.Add(n)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	// Give the other half a moment, then close both.
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	return total.Load()
}

var _ = log.Printf
