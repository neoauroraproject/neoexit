package proxy

// Reading the first bytes a client sends, for routing: the site name
// (TLS SNI, HTTP Host) when the client asked for an IP, and BitTorrent.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// sniffWait is how long to wait for a client's first bytes. Protocols
// where the server speaks first (SSH, SMTP) wait this long once.
const sniffWait = 250 * time.Millisecond

// peek returns the first bytes without taking them from br.
func peek(c net.Conn, br *bufio.Reader) []byte {
	c.SetReadDeadline(time.Now().Add(sniffWait))
	defer c.SetReadDeadline(time.Time{})
	if _, err := br.Peek(1); err != nil {
		return nil
	}
	b, _ := br.Peek(br.Buffered())
	// A TLS ClientHello may come in more than one read.
	if len(b) >= 5 && b[0] == 0x16 {
		need := 5 + int(binary.BigEndian.Uint16(b[3:5]))
		if need > len(b) && need <= 16<<10 {
			if more, err := br.Peek(need); err == nil {
				b = more
			} else {
				b, _ = br.Peek(br.Buffered())
			}
		}
	}
	return b
}

// sniffName finds the site name in a TLS ClientHello or an HTTP request.
func sniffName(b []byte) string {
	if n := sniffTLS(b); n != "" {
		return n
	}
	return sniffHTTP(b)
}

func sniffTLS(b []byte) string {
	// record header, then handshake: type 1 (ClientHello), length(3)
	if len(b) < 9 || b[0] != 0x16 || b[5] != 1 {
		return ""
	}
	p := b[9:]
	skip := func(n int) bool {
		if len(p) < n {
			return false
		}
		p = p[n:]
		return true
	}
	vec := func(lenBytes int) bool {
		if len(p) < lenBytes {
			return false
		}
		n := 0
		for i := 0; i < lenBytes; i++ {
			n = n<<8 | int(p[i])
		}
		return skip(lenBytes + n)
	}
	// version(2) random(32) session id, cipher suites, compression
	if !skip(34) || !vec(1) || !vec(2) || !vec(1) || len(p) < 2 {
		return ""
	}
	p = p[2:] // extensions length
	for len(p) >= 4 {
		typ := binary.BigEndian.Uint16(p)
		n := int(binary.BigEndian.Uint16(p[2:]))
		p = p[4:]
		if len(p) < n {
			return ""
		}
		ext := p[:n]
		p = p[n:]
		if typ != 0 { // server_name
			continue
		}
		// list length(2), then type(1)=0 host_name, length(2), name
		if len(ext) < 5 || ext[2] != 0 {
			return ""
		}
		l := int(binary.BigEndian.Uint16(ext[3:]))
		if len(ext) < 5+l {
			return ""
		}
		return validName(string(ext[5 : 5+l]))
	}
	return ""
}

func sniffHTTP(b []byte) string {
	if !isHTTP(b) {
		return ""
	}
	for _, line := range strings.Split(string(b), "\r\n")[1:] {
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "host") {
			v = strings.TrimSpace(v)
			if h, _, err := net.SplitHostPort(v); err == nil {
				v = h
			}
			return validName(v)
		}
	}
	return ""
}

// validName keeps names only (an IP or junk is no help).
func validName(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	if s == "" || len(s) > 253 || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return ""
	}
	for _, c := range s {
		if !(c == '.' || c == '-' || c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return ""
		}
	}
	return s
}

// sniffProtocol names what a TCP connection starts with.
func sniffProtocol(b []byte) string {
	switch {
	case isTorrent(b):
		return "bittorrent"
	case len(b) >= 3 && b[0] == 0x16 && b[1] == 3:
		return "tls"
	case isHTTP(b):
		return "http"
	}
	return ""
}

// sniffUDP names what a UDP flow's first packet is.
func sniffUDP(b []byte) string {
	switch {
	case isTorrentUDP(b):
		return "bittorrent"
	case isQUIC(b):
		return "quic"
	}
	return ""
}

// isQUIC is a QUIC long-header Initial packet (version 1 or 2).
func isQUIC(b []byte) bool {
	if len(b) < 1200 || b[0]&0xc0 != 0xc0 {
		return false
	}
	v := binary.BigEndian.Uint32(b[1:5])
	return v == 1 || v == 0x6b3343cf
}

func isHTTP(b []byte) bool {
	sp := bytes.IndexByte(b, ' ')
	if sp < 3 || sp > 8 {
		return false
	}
	for _, c := range b[:sp] {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// isTorrent is the BitTorrent handshake over TCP.
func isTorrent(b []byte) bool {
	return bytes.HasPrefix(b, []byte("\x13BitTorrent protocol"))
}

// isTorrentUDP is a DHT message (bencoded, "d1:a..."/"d1:r..." with
// "1:y1:"). uTP has no marker reliable enough to block on.
func isTorrentUDP(b []byte) bool {
	return bytes.HasPrefix(b, []byte("d1:")) && bytes.Contains(b, []byte("1:y1:"))
}
