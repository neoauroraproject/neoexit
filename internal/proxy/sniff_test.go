package proxy

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// clientHello captures what crypto/tls sends first for serverName.
func clientHello(t *testing.T, serverName string) []byte {
	a, b := net.Pipe()
	go func() {
		c := tls.Client(a, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
		c.SetDeadline(time.Now().Add(time.Second))
		c.Handshake()
		a.Close()
	}()
	b.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16<<10)
	n, _ := b.Read(buf)
	b.Close()
	return buf[:n]
}

func TestSniff(t *testing.T) {
	if got := sniffName(clientHello(t, "chatgpt.com")); got != "chatgpt.com" {
		t.Errorf("TLS SNI: %q", got)
	}
	if got := sniffName([]byte("GET / HTTP/1.1\r\nHost: Example.COM:8080\r\nAccept: */*\r\n\r\n")); got != "example.com" {
		t.Errorf("HTTP Host: %q", got)
	}
	for _, junk := range [][]byte{nil, []byte("SSH-2.0-OpenSSH\r\n"), []byte("GET / HTTP/1.1\r\nHost: 1.2.3.4\r\n\r\n"), {0x16, 3, 1, 0, 5, 1, 0}} {
		if got := sniffName(junk); got != "" {
			t.Errorf("%q gave %q", junk, got)
		}
	}
	if !isTorrent([]byte("\x13BitTorrent protocol\x00\x00")) || isTorrent([]byte("GET /")) {
		t.Error("BitTorrent handshake")
	}
	if !isTorrentUDP([]byte("d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:aa1:y1:qe")) || isTorrentUDP([]byte("\x12\x34\x01\x00")) {
		t.Error("DHT")
	}
}

func TestSniffProtocol(t *testing.T) {
	if p := sniffProtocol(clientHello(t, "x.com")); p != "tls" {
		t.Errorf("tls: %q", p)
	}
	if p := sniffProtocol([]byte("POST /x HTTP/1.1\r\n")); p != "http" {
		t.Errorf("http: %q", p)
	}
	if p := sniffProtocol([]byte("\x13BitTorrent protocol")); p != "bittorrent" {
		t.Errorf("bt: %q", p)
	}
	if p := sniffProtocol([]byte("SSH-2.0-x")); p != "" {
		t.Errorf("ssh: %q", p)
	}
	q := make([]byte, 1200)
	q[0], q[4] = 0xc3, 1 // long header, version 1
	if p := sniffUDP(q); p != "quic" {
		t.Errorf("quic: %q", p)
	}
	if p := sniffUDP(q[:100]); p != "" {
		t.Errorf("short udp: %q", p)
	}
}
