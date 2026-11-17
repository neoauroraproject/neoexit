package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"neoexit/internal/config"
)

// HostInfo is what else lives on this server.
type HostInfo struct {
	Hostname  string `json:"hostname"`
	NeoTunnel bool   `json:"neotunnel"`
	XUI       bool   `json:"xui"` // 3x-ui
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func Host() HostInfo {
	h, _ := os.Hostname()
	return HostInfo{
		Hostname:  h,
		NeoTunnel: exists("/usr/local/bin/neotunnel") || exists("/etc/neotunnel"),
		XUI:       exists("/etc/x-ui/x-ui.db") || exists("/usr/local/x-ui"),
	}
}

// Snippet is what to paste into 3x-ui (Xray): an outbound to the local
// proxy and a routing rule sending the AI services' domains to it.
func Snippet(cfg config.Config) map[string]any {
	server := map[string]any{"address": "127.0.0.1", "port": cfg.Proxy.Port}
	if cfg.Proxy.User != "" {
		server["users"] = []map[string]string{{"user": cfg.Proxy.User, "pass": cfg.Proxy.Pass}}
	}
	var domains []string
	for _, s := range cfg.Services {
		if !s.Enabled {
			continue
		}
		for _, d := range s.Domains {
			domains = append(domains, "domain:"+d)
		}
	}
	return map[string]any{
		"outbound": map[string]any{"tag": "neoexit", "protocol": "socks", "settings": map[string]any{"servers": []any{server}}},
		"rule":     map[string]any{"type": "field", "outboundTag": "neoexit", "domain": domains},
	}
}

// --- local API for NeoTunnel -------------------------------------------

// SocketPath is where the local API listens. Only root can open it, so it
// needs no password; it never listens on the network.
const SocketPath = "/run/neoexit/api.sock"

// StatusV1 is the contract NeoTunnel reads. Fields are only ever added.
type StatusV1 struct {
	Version  string            `json:"version"`
	Panel    string            `json:"panel"` // ":port/path/" — NeoTunnel adds the server's address
	Proxy    string            `json:"proxy"` // listen:port
	Services []ServiceStatusV1 `json:"services"`
	Exits    []ExitStatusV1    `json:"exits"`
	LastRun  time.Time         `json:"last_run"`
}

type ServiceStatusV1 struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // ok, geo, challenge, error, down
	Exit  string `json:"exit"`  // exit name
}

type ExitStatusV1 struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Up      bool   `json:"up"`
	IP      string `json:"ip,omitempty"`
	Country string `json:"country,omitempty"`
	Ms      int    `json:"ms,omitempty"`
}

func (a *App) LocalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.StatusV1())
	})
	mux.HandleFunc("POST /v1/check", func(w http.ResponseWriter, r *http.Request) {
		a.Engine.CheckNow()
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/rotate", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Engine.Rotate(r.Context(), r.URL.Query().Get("exit")); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	return mux
}

func (a *App) StatusV1() StatusV1 {
	cfg := a.Store.Get()
	live := a.Engine.Snapshot()
	s := StatusV1{
		Version: a.Version,
		Panel:   ":" + itoa(cfg.Panel.Port) + "/" + cfg.Panel.Path + "/",
		Proxy:   net.JoinHostPort(cfg.Proxy.Listen, itoa(cfg.Proxy.Port)),
		LastRun: live.LastRun,
	}
	names := map[string]string{}
	for _, x := range cfg.Exits {
		names[x.ID] = x.Name
	}
	for _, x := range live.Exits {
		ex := cfg.Exit(x.ID)
		if ex == nil {
			continue
		}
		s.Exits = append(s.Exits, ExitStatusV1{ID: x.ID, Name: ex.Name, Kind: ex.Kind, Up: x.Up && ex.Enabled, IP: x.Trace.IP, Country: x.Trace.Country, Ms: x.Trace.Ms})
	}
	for _, sv := range cfg.Services {
		if !sv.Enabled {
			continue
		}
		st := ServiceStatusV1{ID: sv.ID, Name: sv.Name}
		for _, l := range live.Services {
			if l.ID == sv.ID {
				st.State, st.Exit = l.State, names[l.Exit]
			}
		}
		s.Services = append(s.Services, st)
	}
	return s
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ListenLocal opens the unix socket, readable by root only.
func ListenLocal(path string) (net.Listener, error) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	os.Chmod(path, 0o600)
	return ln, nil
}

// --- panel certificate --------------------------------------------------

// Certificate loads or makes the panel's self-signed certificate.
func Certificate(dir string) (tls.Certificate, error) {
	cp, kp := filepath.Join(dir, "panel.crt"), filepath.Join(dir, "panel.key")
	if c, err := tls.LoadX509KeyPair(cp, kp); err == nil {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "NeoExit"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return tls.LoadX509KeyPair(cp, kp)
}

var _ = strings.TrimSpace
