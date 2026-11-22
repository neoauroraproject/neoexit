package proxy

// SOCKS5 UDP ASSOCIATE (RFC 1928 §7), so voice calls, games and QUIC from
// 3x-ui can go through NeoExit too. Each destination is routed like a
// TCP connection to it; UDP can leave only through the server's own route
// or an exit that carries UDP (its own IPv4/IPv6, WARP), and is dropped
// everywhere else. The association lives as long as its TCP connection.

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"neoexit/internal/dns"
	"neoexit/internal/exit"
	"neoexit/internal/route"
)

const (
	udpIdle    = 2 * time.Minute // an idle destination is let go
	udpMaxDest = 512             // destinations per association
)

func (s *Server) associate(c net.Conn, br *bufio.Reader) {
	local, _ := c.LocalAddr().(*net.TCPAddr)
	peer, _ := c.RemoteAddr().(*net.TCPAddr)
	if local == nil || peer == nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	// The relay listens on the address the client reached us at.
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: local.IP})
	if err != nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	bnd := pc.LocalAddr().(*net.UDPAddr)
	reply := []byte{5, 0, 0}
	if ip4 := bnd.IP.To4(); ip4 != nil {
		reply = append(append(reply, 1), ip4...)
	} else {
		reply = append(append(reply, 4), bnd.IP.To16()...)
	}
	reply = binary.BigEndian.AppendUint16(reply, uint16(bnd.Port))
	if _, err := c.Write(reply); err != nil {
		pc.Close()
		return
	}
	c.SetDeadline(time.Time{})

	r := &relay{s: s, pc: pc, peerIP: peer.IP, dests: map[string]*udpDest{}}
	go r.run()
	// The association ends with its TCP connection.
	io.Copy(io.Discard, br)
	r.close()
}

type relay struct {
	s      *Server
	pc     *net.UDPConn
	peerIP net.IP

	mu     sync.Mutex
	client *net.UDPAddr // where replies go: the first packet's source
	dests  map[string]*udpDest
	closed bool
}

type udpDest struct {
	conn  net.Conn // nil: dropped (no exit here carries UDP)
	head  []byte   // the SOCKS header replies carry
	last  time.Time
	dec   route.Decision
	bytes atomic.Int64
}

func (r *relay) run() {
	buf := make([]byte, 65535)
	for {
		n, from, err := r.pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Only the client that asked may use the relay.
		if !from.IP.Equal(r.peerIP) {
			continue
		}
		host, port, data, head, ok := parseUDP(buf[:n])
		if !ok {
			continue
		}
		r.mu.Lock()
		if r.client == nil {
			r.client = from
		}
		r.mu.Unlock()
		d := r.dest(host, port, head, data)
		if d == nil || d.conn == nil {
			continue
		}
		if n, err := d.conn.Write(data); err == nil {
			d.bytes.Add(int64(n))
		}
	}
}

// dest finds or opens the upstream for one destination.
func (r *relay) dest(host, port string, head, first []byte) *udpDest {
	key := net.JoinHostPort(host, port)
	r.mu.Lock()
	if d := r.dests[key]; d != nil {
		// A dropped destination is tried again after a while (its exit
		// may have changed).
		if d.conn != nil || time.Since(d.last) < 30*time.Second {
			if d.conn != nil {
				d.last = time.Now()
			}
			r.mu.Unlock()
			return d
		}
		delete(r.dests, key)
	}
	if r.closed || len(r.dests) >= udpMaxDest {
		r.mu.Unlock()
		return nil
	}
	d := &udpDest{head: head, last: time.Now()}
	r.dests[key] = d
	r.mu.Unlock()

	pn, _ := strconv.Atoi(port)
	dl, dec := r.s.decide(route.Request{Host: host, Port: pn, Network: "udp", Protocol: sniffUDP(first), Proxy: r.s.ID})
	d.dec = dec
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var conn net.Conn
	var err error
	switch u, ok := dl.(exit.UDPDialer); {
	case dl == nil:
		conn, err = dns.Dial(ctx, "udp", key)
	case ok:
		conn, err = u.DialUDP(ctx, key)
	default:
		r.s.count(dec, 0)
		return d // this exit can't carry UDP: drop
	}
	if err != nil {
		r.s.count(dec, 0)
		return d
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		conn.Close()
		return nil
	}
	d.conn = conn
	r.mu.Unlock()
	go r.back(key, d)
	return d
}

// back sends an upstream's replies to the client until it goes idle.
func (r *relay) back(key string, d *udpDest) {
	buf := make([]byte, 65535)
	off := copy(buf, d.head)
	for {
		d.conn.SetReadDeadline(time.Now().Add(udpIdle))
		n, err := d.conn.Read(buf[off:])
		if err != nil {
			r.mu.Lock()
			busy := !r.closed && time.Since(d.last) < udpIdle
			if ne, ok := err.(net.Error); ok && ne.Timeout() && busy {
				r.mu.Unlock()
				continue // packets still go out: keep it
			}
			if r.dests[key] == d {
				delete(r.dests, key)
			}
			r.mu.Unlock()
			d.conn.Close()
			r.s.count(d.dec, d.bytes.Load())
			return
		}
		d.bytes.Add(int64(n))
		r.mu.Lock()
		to := r.client
		d.last = time.Now()
		r.mu.Unlock()
		if to != nil {
			r.pc.WriteToUDP(buf[:off+n], to)
		}
	}
}

func (r *relay) close() {
	r.mu.Lock()
	r.closed = true
	for k, d := range r.dests {
		if d.conn != nil {
			d.conn.Close()
		}
		delete(r.dests, k)
	}
	r.mu.Unlock()
	r.pc.Close()
}

// parseUDP reads a SOCKS5 UDP request: RSV(2) FRAG ATYP DST.ADDR DST.PORT
// DATA. Fragments are not supported (no client sends them). head is the
// header up to the data, reused for replies.
func parseUDP(b []byte) (host, port string, data, head []byte, ok bool) {
	if len(b) < 4 || b[2] != 0 {
		return
	}
	i := 4
	switch b[3] {
	case 1:
		if len(b) < i+4+2 {
			return
		}
		host = net.IP(b[i : i+4]).String()
		i += 4
	case 4:
		if len(b) < i+16+2 {
			return
		}
		host = net.IP(b[i : i+16]).String()
		i += 16
	case 3:
		if len(b) < i+1 {
			return
		}
		l := int(b[i])
		if len(b) < i+1+l+2 {
			return
		}
		host = string(b[i+1 : i+1+l])
		i += 1 + l
	default:
		return
	}
	port = strconv.Itoa(int(binary.BigEndian.Uint16(b[i : i+2])))
	i += 2
	head = append([]byte(nil), b[:i]...)
	return host, port, b[i:], head, true
}
