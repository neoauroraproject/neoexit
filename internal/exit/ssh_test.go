package exit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"neoexit/internal/config"
)

// sshServer is a tiny SSH server that only forwards "direct-tcpip"
// channels, as "ssh -D" uses them.
func sshServer(t *testing.T, pass string) (addr string, hostKey ssh.PublicKey) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "u" && string(p) == pass {
			return nil, nil
		}
		return nil, io.EOF
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						nc.Reject(ssh.UnknownChannelType, "")
						continue
					}
					d := nc.ExtraData()
					hl := binary.BigEndian.Uint32(d)
					host := string(d[4 : 4+hl])
					port := binary.BigEndian.Uint32(d[4+hl:])
					up, err := net.Dial("tcp", net.JoinHostPort(host, itoa(int(port))))
					if err != nil {
						nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, rq, _ := nc.Accept()
					go ssh.DiscardRequests(rq)
					go func() { io.Copy(ch, up); ch.CloseWrite() }()
					go func() { io.Copy(up, ch); up.Close() }()
				}
			}()
		}
	}()
	return ln.Addr().String(), signer.PublicKey()
}

func itoa(n int) string {
	var b [12]byte
	i := len(b)
	for n > 0 || i == len(b) {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestSSHExit(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "through ssh") }))
	defer web.Close()
	addr, _ := sshServer(t, "secret")

	learned := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := New(ctx, config.Exit{Kind: config.SSH, Address: addr, User: "u", Pass: "secret"},
		Options{OnHostKey: func(k string) { learned <- k }})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c := &http.Client{Transport: &http.Transport{DialContext: d.DialContext}}
	resp, err := c.Get(web.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "through ssh" {
		t.Fatalf("got %q", b)
	}
	key := <-learned

	// The same server again with the learned key works; a different key
	// (another server at that address) is refused.
	if _, err := New(ctx, config.Exit{Kind: config.SSH, Address: addr, User: "u", Pass: "secret", HostKey: key}, Options{}); err != nil {
		t.Fatalf("known key refused: %v", err)
	}
	_, other := sshServer(t, "x")
	otherKey := base64.StdEncoding.EncodeToString(other.Marshal())
	_, err = New(ctx, config.Exit{Kind: config.SSH, Address: addr, User: "u", Pass: "secret", HostKey: otherKey}, Options{})
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed key accepted: %v", err)
	}
	// A wrong password fails.
	if _, err := New(ctx, config.Exit{Kind: config.SSH, Address: addr, User: "u", Pass: "nope"}, Options{}); err == nil {
		t.Fatal("wrong password accepted")
	}
}
