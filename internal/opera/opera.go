// Package opera uses the free proxies of Opera's browser VPN as an exit,
// in one of three regions: EU (Europe), AS (Asia) or AM (Americas). The
// API client is github.com/Snawoot/opera-proxy's (MIT); the proxies are
// HTTPS proxies that take CONNECT with the device's credentials.
//
// This is not an official API. It may stop working; the panel then shows
// the error and the other exits take over.
package opera

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	se "github.com/Snawoot/opera-proxy/seclient"
)

// Regions Opera offers.
var Regions = []string{"EU", "AS", "AM"}

const (
	apiLogin    = "se0316"
	apiPassword = "SILrMEPBmJuhomxWkfm3JalqHX2Eheg1YhlEZiMh8II"
	// The device password is refreshed well before it expires.
	refreshEvery = 3 * time.Hour
)

// Dialer is one anonymous Opera device and the proxies of its region.
type Dialer struct {
	region string
	client *se.SEClient

	mu        sync.Mutex
	endpoints []string
	user      string
	pass      string
	refreshed time.Time
	stop      chan struct{}
}

// Start registers an anonymous device and finds the region's proxies.
func Start(ctx context.Context, region string) (*Dialer, error) {
	if region == "" {
		region = "EU"
	}
	c, err := se.NewSEClient(apiLogin, apiPassword, &http.Transport{
		Proxy:               nil,
		TLSHandshakeTimeout: 15 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	if err := c.AnonRegister(ctx); err != nil {
		return nil, fmt.Errorf("Opera: register: %w", err)
	}
	if err := c.RegisterDevice(ctx); err != nil {
		return nil, fmt.Errorf("Opera: device: %w", err)
	}
	ips, err := c.Discover(ctx, fmt.Sprintf("\"%s\",,", region))
	if err != nil {
		return nil, fmt.Errorf("Opera: discover: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("Opera has no proxies in %s right now", region)
	}
	d := &Dialer{region: region, client: c, stop: make(chan struct{}), refreshed: time.Now()}
	for _, ip := range ips {
		d.endpoints = append(d.endpoints, ip.NetAddr())
	}
	d.user, d.pass = c.GetProxyCredentials()
	go d.keepFresh()
	return d, nil
}

func (d *Dialer) keepFresh() {
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if d.client.Login(ctx) == nil && d.client.DeviceGeneratePassword(ctx) == nil {
			u, p := d.client.GetProxyCredentials()
			d.mu.Lock()
			d.user, d.pass, d.refreshed = u, p, time.Now()
			d.mu.Unlock()
		}
		cancel()
	}
}

// DialContext opens a CONNECT tunnel through one of the region's proxies,
// trying the next one if a proxy doesn't answer.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	eps := append([]string(nil), d.endpoints...)
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(d.user+":"+d.pass))
	d.mu.Unlock()
	rand.Shuffle(len(eps), func(i, j int) { eps[i], eps[j] = eps[j], eps[i] })
	var last error
	for i, ep := range eps {
		if i == 3 {
			break
		}
		c, err := d.dialOne(ctx, ep, address, auth)
		if err == nil {
			return c, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

func (d *Dialer) dialOne(ctx context.Context, ep, address, auth string) (net.Conn, error) {
	var nd net.Dialer
	nd.Timeout = 10 * time.Second
	raw, err := nd.DialContext(ctx, "tcp", ep)
	if err != nil {
		return nil, err
	}
	// Opera's proxies want no SNI; their certificate is checked against
	// the region's name instead.
	name := fmt.Sprintf("%s0.sec-tunnel.com", d.region)
	conn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("Opera proxy sent no certificate")
			}
			opts := x509.VerifyOptions{DNSName: name, Intermediates: x509.NewCertPool()}
			for _, c := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(c)
			}
			_, err := cs.PeerCertificates[0].Verify(opts)
			return err
		},
	})
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	req := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\nProxy-Authorization: " + auth + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		raw.Close()
		return nil, err
	}
	if err := readConnectReply(conn); err != nil {
		raw.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return conn, nil
}

// readConnectReply reads the proxy's answer byte by byte up to the blank
// line, so nothing of the tunnelled stream is swallowed.
func readConnectReply(c net.Conn) error {
	var buf []byte
	b := make([]byte, 1)
	for len(buf) < 8192 {
		if _, err := c.Read(b); err != nil {
			return err
		}
		buf = append(buf, b[0])
		if n := len(buf); n >= 4 && string(buf[n-4:]) == "\r\n\r\n" {
			break
		}
	}
	// "HTTP/1.1 200 ..."
	if len(buf) < 12 || string(buf[9:12]) != "200" {
		line := string(buf)
		if i := indexCRLF(line); i > 0 {
			line = line[:i]
		}
		return fmt.Errorf("Opera proxy refused: %s", line)
	}
	return nil
}

func indexCRLF(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\r' && s[i+1] == '\n' {
			return i
		}
	}
	return -1
}

func (d *Dialer) Close() error {
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
	return nil
}
