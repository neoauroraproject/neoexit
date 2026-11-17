// Package psiphon runs Psiphon's own console client as a separate
// process and uses its local SOCKS port as an exit, with the exit country
// chosen (EgressRegion). Psiphon's code is GPLv3, so it is never linked
// into NeoExit: the program is downloaded from Psiphon's repository the
// first time a Psiphon exit is added, and only talked to over SOCKS.
//
// The configuration is the public example one from Psiphon's own client
// library (the "FFFFFFFFFFFFFFFF" propagation channel and sponsor).
package psiphon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Countries Psiphon has exits in (empty = whichever is best).
var Countries = []string{"", "AT", "AU", "BE", "BG", "CA", "CH", "CZ", "DE", "DK", "EE", "ES", "FI", "FR", "GB", "HR", "HU",
	"IE", "IN", "IT", "JP", "LV", "NL", "NO", "PL", "PT", "RO", "RS", "SE", "SG", "SK", "US"}

const (
	signatureKey  = "MIICIDANBgkqhkiG9w0BAQEFAAOCAg0AMIICCAKCAgEAt7Ls+/39r+T6zNW7GiVpJfzq/xvL9SBH5rIFnk0RXYEYavax3WS6HOD35eTAqn8AniOwiH+DOkvgSKF2caqk/y1dfq47Pdymtwzp9ikpB1C5OfAysXzBiwVJlCdajBKvBZDerV1cMvRzCKvKwRmvDmHgphQQ7WfXIGbRbmmk6opMBh3roE42KcotLFtqp0RRwLtcBRNtCdsrVsjiI1Lqz/lH+T61sGjSjQ3CHMuZYSQJZo/KrvzgQXpkaCTdbObxHqb6/+i1qaVOfEsvjoiyzTxJADvSytVtcTjijhPEV6XskJVHE1Zgl+7rATr/pDQkw6DPCNBS1+Y6fy7GstZALQXwEDN/qhQI9kWkHijT8ns+i1vGg00Mk/6J75arLhqcodWsdeG/M/moWgqQAnlZAGVtJI1OgeF5fsPpXu4kctOfuZlGjVZXQNW34aOzm8r8S0eVZitPlbhcPiR4gT/aSMz/wd8lZlzZYsje/Jr8u/YtlwjjreZrGRmG8KMOzukV3lLmMppXFMvl4bxv6YFEmIuTsOhbLTwFgh7KYNjodLj/LsqRVfwz31PgWQFTEPICV7GCvgVlPRxnofqKSjgTWI4mxDhBpVcATvaoBl1L/6WLbFvBsoAUBItWwctO2xalKxF5szhGm8lccoc5MZr8kfE0uxMgsxz4er68iCID+rsCAQM="
	serverListURL = "https://s3.amazonaws.com//psiphon/web/mjr4-p23r-puwl/server_list_compressed"
)

// binaryURLs: NeoExit's own build first (amd64 and arm64), then Psiphon's
// official one (x86_64 Linux, i686 Windows for testing).
func binaryURLs() []string {
	var out []string
	if runtime.GOOS == "linux" {
		out = append(out, "https://github.com/neoauroraproject/neoexit/releases/latest/download/psiphon-linux-"+runtime.GOARCH)
		if runtime.GOARCH == "amd64" {
			out = append(out, "https://raw.githubusercontent.com/Psiphon-Labs/psiphon-tunnel-core-binaries/master/linux/psiphon-tunnel-core-x86_64")
		}
	}
	if runtime.GOOS == "windows" {
		out = append(out, "https://raw.githubusercontent.com/Psiphon-Labs/psiphon-tunnel-core-binaries/master/windows/psiphon-tunnel-core-i686.exe")
	}
	return out
}

var fetchMu sync.Mutex

// Binary returns the client program, downloading it into dir once.
func Binary(ctx context.Context, dir string) (string, error) {
	fetchMu.Lock()
	defer fetchMu.Unlock()
	name := "psiphon-client"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if st, err := os.Stat(path); err == nil && st.Size() > 1<<20 {
		return path, nil
	}
	urls := binaryURLs()
	if len(urls) == 0 {
		return "", fmt.Errorf("no Psiphon client for %s/%s; put one at %s", runtime.GOOS, runtime.GOARCH, path)
	}
	os.MkdirAll(dir, 0o700)
	var last error
	for _, u := range urls {
		if last = download(ctx, u, path); last == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("Psiphon client download failed (%v); or put one at %s", last, path)
}

func download(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20))
	f.Close()
	if err != nil || n < 1<<20 {
		os.Remove(tmp)
		if err == nil {
			err = errors.New("file too small")
		}
		return err
	}
	return os.Rename(tmp, path)
}

// Client is one running Psiphon client.
type Client struct {
	country string
	dir     string
	port    int

	mu      sync.Mutex
	ready   bool
	lastErr string
	cmd     *exec.Cmd
	stop    chan struct{}
	stopped bool
}

// Start runs the client in the background (downloading it first if
// needed) and keeps it running. Dial fails until it has a tunnel.
func Start(ctx context.Context, dataDir, id, country string) (*Client, error) {
	bin, err := Binary(ctx, filepath.Join(dataDir, "psiphon"))
	if err != nil {
		return nil, err
	}
	// Absolute: the client runs inside this folder and reads the path too.
	dir, err := filepath.Abs(filepath.Join(dataDir, "psiphon", id))
	if err != nil {
		return nil, err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	c := &Client{country: country, dir: dir, port: port, stop: make(chan struct{})}
	go c.supervise(bin)
	return c, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (c *Client) writeConfig() (string, error) {
	cfg := map[string]any{
		"PropagationChannelId":               "FFFFFFFFFFFFFFFF",
		"SponsorId":                          "FFFFFFFFFFFFFFFF",
		"RemoteServerListUrl":                serverListURL,
		"RemoteServerListSignaturePublicKey": signatureKey,
		"RemoteServerListDownloadFilename":   "remote_server_list",
		"ClientPlatform":                     "Linux_NeoExit",
		"NetworkID":                          "neoexit",
		"EgressRegion":                       c.country,
		"LocalSocksProxyPort":                c.port,
		"DisableLocalHTTPProxy":              true,
		"DataRootDirectory":                  c.dir,
		"EmitDiagnosticNotices":              false,
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	p := filepath.Join(c.dir, "config.json")
	return p, os.WriteFile(p, b, 0o600)
}

func (c *Client) supervise(bin string) {
	backoff := 3 * time.Second
	for {
		started := time.Now()
		err := c.runOnce(bin)
		c.mu.Lock()
		c.ready = false
		if err != nil {
			c.lastErr = err.Error()
		}
		stopped := c.stopped
		c.mu.Unlock()
		if stopped {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = 3 * time.Second
		} else if backoff < 2*time.Minute {
			backoff *= 2
		}
		select {
		case <-c.stop:
			return
		case <-time.After(backoff):
		}
	}
}

type notice struct {
	Type string         `json:"noticeType"`
	Data map[string]any `json:"data"`
}

func (c *Client) runOnce(bin string) error {
	cfg, err := c.writeConfig()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "-config", cfg)
	cmd.Dir = c.dir
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	c.mu.Lock()
	c.cmd = cmd
	c.mu.Unlock()
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var n notice
		if json.Unmarshal(sc.Bytes(), &n) != nil {
			continue
		}
		switch n.Type {
		case "Tunnels":
			cnt, _ := n.Data["count"].(float64)
			c.mu.Lock()
			c.ready = cnt > 0
			if c.ready {
				c.lastErr = ""
			}
			c.mu.Unlock()
		case "ListeningSocksProxyPort":
			if p, ok := n.Data["port"].(float64); ok {
				c.mu.Lock()
				c.port = int(p)
				c.mu.Unlock()
			}
		case "Alert", "Error":
			if msg, ok := n.Data["message"].(string); ok {
				c.mu.Lock()
				c.lastErr = msg
				c.mu.Unlock()
			}
		}
	}
	return cmd.Wait()
}

// DialContext goes through Psiphon's SOCKS port once it has a tunnel.
func (c *Client) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c.mu.Lock()
	ready, port, last := c.ready, c.port, c.lastErr
	c.mu.Unlock()
	if !ready {
		if last != "" {
			return nil, errors.New("Psiphon is connecting (" + last + ")")
		}
		return nil, errors.New("Psiphon is connecting")
	}
	d, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(port), nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return d.(proxy.ContextDialer).DialContext(ctx, "tcp", address)
}

// WaitReady waits up to d for the first tunnel.
func (c *Client) WaitReady(ctx context.Context) bool {
	for {
		c.mu.Lock()
		r := c.ready
		c.mu.Unlock()
		if r {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	close(c.stop)
	cmd := c.cmd
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	return nil
}
