// Package warp registers free Cloudflare WARP accounts and runs them in
// userspace (wireguard-go on a gVisor network stack). Nothing on the
// server changes: no interface, no route, so SSH and other programs keep
// their normal path; only connections dialed through Device use WARP.
package warp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"neoexit/internal/config"
)

// The client API the official apps use. It is not documented and may
// change; errors from it are shown in the panel as they come.
const (
	apiBase       = "https://api.cloudflareclient.com/v0a2158"
	clientVersion = "a-6.11-2223"
	userAgent     = "okhttp/3.12.1"
)

// Fallback endpoint when engage.cloudflareclient.com doesn't resolve.
const fallbackEndpoint = "162.159.192.1:2408"

func apiClient(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	tr := &http.Transport{
		// The API is picky about TLS clients; the official app uses 1.2.
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   false,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if dial != nil {
		tr.DialContext = dial
	}
	return &http.Client{Transport: tr, Timeout: 30 * time.Second}
}

func call(ctx context.Context, c *http.Client, method, url, token string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("CF-Client-Version", clientVersion)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("WARP API %s: %s", resp.Status, msg)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type regResponse struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Account struct {
		AccountType string `json:"account_type"`
		WarpPlus    bool   `json:"warp_plus"`
	} `json:"account"`
	Config struct {
		Peers []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				Host string `json:"host"`
				V4   string `json:"v4"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// Register creates a new free account. A new account usually gets a new
// exit IP, which is how "new IP" works. license, if set, upgrades it to
// WARP+.
func Register(ctx context.Context, license string) (*config.WARPAccount, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return nil, err
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	c := apiClient(nil)
	var reg regResponse
	body := map[string]any{
		"key":           base64.StdEncoding.EncodeToString(pub),
		"install_id":    "",
		"fcm_token":     "",
		"tos":           time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"model":         "PC",
		"type":          "Android",
		"locale":        "en_US",
		"serial_number": "",
		"warp_enabled":  true,
	}
	if err := call(ctx, c, http.MethodPost, apiBase+"/reg", "", body, &reg); err != nil {
		return nil, err
	}
	if reg.ID == "" || len(reg.Config.Peers) == 0 {
		return nil, errors.New("WARP API: registration answer has no peer")
	}
	a := &config.WARPAccount{
		ID:         reg.ID,
		Token:      reg.Token,
		PrivateKey: base64.StdEncoding.EncodeToString(priv[:]),
		PeerKey:    reg.Config.Peers[0].PublicKey,
		Endpoint:   reg.Config.Peers[0].Endpoint.Host,
		V4:         reg.Config.Interface.Addresses.V4,
		V6:         reg.Config.Interface.Addresses.V6,
	}
	if license != "" {
		var acc struct {
			WarpPlus bool `json:"warp_plus"`
		}
		if err := call(ctx, c, http.MethodPut, apiBase+"/reg/"+reg.ID+"/account", reg.Token, map[string]string{"license": license}, &acc); err != nil {
			return a, fmt.Errorf("account works, but the WARP+ key was refused: %w", err)
		}
		a.Plus = acc.WarpPlus
	}
	return a, nil
}

// Device is one running WARP account.
type Device struct {
	dev *device.Device
	net *netstack.Net
}

func Start(a *config.WARPAccount) (*Device, error) {
	var addrs []netip.Addr
	for _, s := range []string{a.V4, a.V6} {
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			addrs = append(addrs, p.Addr())
		} else if ip, err := netip.ParseAddr(s); err == nil {
			addrs = append(addrs, ip)
		}
	}
	if len(addrs) == 0 {
		return nil, errors.New("WARP account has no address")
	}
	dns := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	tdev, tnet, err := netstack.CreateNetTUN(addrs, dns, 1280)
	if err != nil {
		return nil, err
	}
	priv, err := keyHex(a.PrivateKey)
	if err != nil {
		return nil, err
	}
	peer, err := keyHex(a.PeerKey)
	if err != nil {
		return nil, err
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	uapi := fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=0.0.0.0/0\nallowed_ip=::/0\npersistent_keepalive_interval=25\n",
		priv, peer, resolveEndpoint(a.Endpoint))
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, err
	}
	return &Device{dev: dev, net: tnet}, nil
}

func (d *Device) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.net.DialContext(ctx, network, addr)
}

func (d *Device) DialUDP(ctx context.Context, addr string) (net.Conn, error) {
	return d.net.DialContext(ctx, "udp", addr)
}

func (d *Device) Close() error {
	d.dev.Close()
	return nil
}

func keyHex(b64 string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != 32 {
		return "", errors.New("bad WireGuard key")
	}
	return hex.EncodeToString(b), nil
}

// resolveEndpoint turns host:port into ip:port, as WireGuard wants.
func resolveEndpoint(ep string) string {
	host, port, err := net.SplitHostPort(ep)
	if err != nil {
		return fallbackEndpoint
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return ep
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return fallbackEndpoint
	}
	return net.JoinHostPort(ips[0].String(), port)
}
