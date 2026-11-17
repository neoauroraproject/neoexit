package exit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshExit sends connections out of another server over SSH (like
// "ssh -D"): nothing has to be installed there. The server's host key is
// learned on the first connection (OnHostKey saves it) and must match
// from then on.
type sshExit struct {
	addr, user, pass, key, hostKey string
	onHostKey                      func(string)

	mu     sync.Mutex
	client *ssh.Client
}

func (s *sshExit) connect(ctx context.Context) (*ssh.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	var auth []ssh.AuthMethod
	if s.key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(s.key))
		if err != nil {
			return nil, fmt.Errorf("SSH key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if s.pass != "" {
		auth = append(auth, ssh.Password(s.pass))
	}
	cfg := &ssh.ClientConfig{
		User:    s.user,
		Auth:    auth,
		Timeout: 12 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			got := base64.StdEncoding.EncodeToString(k.Marshal())
			if s.hostKey == "" {
				s.hostKey = got
				if s.onHostKey != nil {
					go s.onHostKey(got)
				}
				return nil
			}
			if got != s.hostKey {
				return errors.New("the server's SSH key changed; remove and add this exit again if that's expected")
			}
			return nil
		},
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, err
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, s.addr, cfg)
	if err != nil {
		raw.Close()
		return nil, err
	}
	c := ssh.NewClient(conn, chans, reqs)
	s.client = c
	go func() {
		c.Wait()
		s.mu.Lock()
		if s.client == c {
			s.client = nil
		}
		s.mu.Unlock()
	}()
	// Keep the connection alive through NATs and idle timeouts.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				c.Close()
				return
			}
		}
	}()
	return c, nil
}

func (s *sshExit) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	for try := 0; try < 2; try++ {
		c, err := s.connect(ctx)
		if err != nil {
			return nil, err
		}
		conn, err := c.DialContext(ctx, "tcp", addr)
		if err == nil {
			return conn, nil
		}
		// A dead connection: drop it and try once more on a fresh one.
		var oe *net.OpError
		if !errors.As(err, &oe) {
			s.mu.Lock()
			if s.client == c {
				s.client = nil
				c.Close()
			}
			s.mu.Unlock()
			continue
		}
		return nil, err
	}
	return nil, errors.New("SSH connection lost")
}

func (s *sshExit) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	return nil
}
