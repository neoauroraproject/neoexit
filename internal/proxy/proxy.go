// Package proxy is the local SOCKS5 and HTTP proxy (one port; the first
// byte tells them apart). Each connection is routed by its host name:
// AI services leave through the exit the engine chose for them, the rest
// through the exit for other traffic or the server's normal route.
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

	"neoexit/internal/exit"
)

// Router picks the dialer for a host; nil means the server's own route.
type Router func(host string) (exit.Dialer, string)

type Server struct {
	Route      Router
	User, Pass string

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

func (s *Server) dial(ctx context.Context, host, port string) (net.Conn, error) {
	addr := net.JoinHostPort(host, port)
	d, _ := s.Route(host)
	if d == nil {
		var nd net.Dialer
		nd.Timeout = 10 * time.Second
		return nd.DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "tcp", addr)
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
	if req[1] != 1 { // CONNECT only
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
	port := strconv.Itoa(int(binary.BigEndian.Uint16(pb)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	up, err := s.dial(ctx, host, port)
	cancel()
	if err != nil {
		c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	c.SetDeadline(time.Time{})
	pipe(c, br, up)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if req.Method == http.MethodConnect {
		host, port, err := net.SplitHostPort(req.Host)
		if err != nil {
			host, port = req.Host, "443"
		}
		up, err := s.dial(ctx, host, port)
		if err != nil {
			io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer up.Close()
		io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		c.SetDeadline(time.Time{})
		pipe(c, br, up)
		return
	}
	// Plain HTTP with an absolute URL.
	if req.URL.Host == "" {
		io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		return
	}
	host, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		host, port = req.URL.Host, "80"
	}
	up, err := s.dial(ctx, host, port)
	if err != nil {
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
	pipe(c, br, up)
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

func pipe(c net.Conn, br *bufio.Reader, up net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(up, br)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(c, up)
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
}

var _ = log.Printf
