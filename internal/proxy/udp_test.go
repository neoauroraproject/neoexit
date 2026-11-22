package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"neoexit/internal/exit"
	"neoexit/internal/netguard"
	"neoexit/internal/route"
)

type tcpOnly struct{}

func (tcpOnly) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("no")
}
func (tcpOnly) Close() error { return nil }

func echoUDP(t *testing.T) *net.UDPAddr {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		b := make([]byte, 2048)
		for {
			n, a, err := pc.ReadFromUDP(b)
			if err != nil {
				return
			}
			pc.WriteToUDP(append([]byte("echo:"), b[:n]...), a)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr)
}

// associate does the SOCKS5 handshake (with a login) and returns the TCP
// connection and the relay address.
func associate(t *testing.T, addr string) (net.Conn, *net.UDPAddr) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write([]byte{5, 1, 2})
	b := make([]byte, 2)
	io.ReadFull(c, b)
	if b[1] != 2 {
		t.Fatalf("method %v", b)
	}
	c.Write([]byte{1, 1, 'u', 1, 'p'})
	io.ReadFull(c, b)
	if b[1] != 0 {
		t.Fatal("login refused")
	}
	c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	r := make([]byte, 10)
	if _, err := io.ReadFull(c, r); err != nil || r[1] != 0 || r[3] != 1 {
		t.Fatalf("associate reply %v %v", r, err)
	}
	return c, &net.UDPAddr{IP: net.IP(r[4:8]), Port: int(binary.BigEndian.Uint16(r[8:10]))}
}

func packet(dst *net.UDPAddr, data string) []byte {
	p := append([]byte{0, 0, 0, 1}, dst.IP.To4()...)
	p = binary.BigEndian.AppendUint16(p, uint16(dst.Port))
	return append(p, data...)
}

func TestUDPAssociate(t *testing.T) {
	echo := echoUDP(t)
	// The echo server is on loopback, which proxies may not reach unless
	// allowed; first without, then allowed.
	netguard.SetAllow(nil)
	defer netguard.SetAllow(nil)
	{
		s := &Server{User: "u", Pass: "p"}
		if err := s.Listen("127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		c, relay := associate(t, s.ln.Addr().String())
		u, _ := net.DialUDP("udp", nil, relay)
		u.SetDeadline(time.Now().Add(700 * time.Millisecond))
		u.Write(packet(echo, "hi"))
		if _, err := u.Read(make([]byte, 100)); err == nil {
			t.Fatal("UDP reached loopback without being allowed")
		}
		u.Close()
		c.Close()
		s.Close()
	}
	netguard.SetAllow([]string{"127.0.0.1"})
	var rt Router = func(route.Request) (exit.Dialer, route.Decision) { return nil, route.Decision{} }
	s := &Server{User: "u", Pass: "p", Route: func(q route.Request) (exit.Dialer, route.Decision) { return rt(q) }}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addr := s.ln.Addr().String()

	c, relay := associate(t, addr)
	defer c.Close()
	u, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(3 * time.Second))
	u.Write(packet(echo, "hi"))
	b := make([]byte, 2048)
	n, err := u.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	head := packet(echo, "")
	if string(b[:len(head)]) != string(head) || string(b[len(head):n]) != "echo:hi" {
		t.Fatalf("reply %q", b[:n])
	}

	// An exit that can't carry UDP: the packet is dropped.
	rt = func(route.Request) (exit.Dialer, route.Decision) { return tcpOnly{}, route.Decision{} }
	c2, relay2 := associate(t, addr)
	defer c2.Close()
	u2, _ := net.DialUDP("udp", nil, relay2)
	defer u2.Close()
	u2.SetDeadline(time.Now().Add(700 * time.Millisecond))
	u2.Write(packet(echo, "hi"))
	if _, err := u2.Read(b); err == nil {
		t.Fatal("UDP went out through a TCP-only exit")
	}

	// Closing the TCP connection ends the association.
	c.Close()
	time.Sleep(200 * time.Millisecond)
	u.SetDeadline(time.Now().Add(500 * time.Millisecond))
	u.Write(packet(echo, "again"))
	if _, err := u.Read(b); err == nil {
		t.Fatal("relay still open after its TCP connection closed")
	}
}
