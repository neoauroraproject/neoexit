// Package config is NeoExit's settings file: the panel, the local proxy,
// the exits traffic can leave through and the AI services it watches.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

// Exit kinds.
const (
	Direct4 = "direct4" // the server's own IPv4
	Direct6 = "direct6" // the server's own IPv6
	WARP    = "warp"    // a free Cloudflare WARP account (WireGuard, userspace)
	SOCKS5  = "socks5"  // any SOCKS5 proxy (Psiphon, a paid proxy, another tool)
	HTTP    = "http"    // any HTTP CONNECT proxy
	Opera   = "opera"   // Opera's free browser VPN proxies (EU, AS, AM)
	Psiphon = "psiphon" // Psiphon's client, run as its own process
	SSH     = "ssh"     // another server of yours, over SSH
)

// Kinds lists the kinds a user can add from the panel.
var Kinds = []string{WARP, Psiphon, Opera, SSH, SOCKS5, HTTP, Direct4, Direct6}

type Config struct {
	Panel    Panel     `json:"panel"`
	Proxy    Proxy     `json:"proxy"`
	Exits    []Exit    `json:"exits"`    // in priority order
	Services []Service `json:"services"` // the AI services checked and routed

	// CheckMinutes is how often every exit is tested against every service.
	CheckMinutes int `json:"check_minutes"`
	// SameCountry: when a service has to move, prefer an exit in the
	// country it used before. Accounts whose country keeps jumping get
	// flagged more often.
	SameCountry bool `json:"same_country"`
	// AutoRotate: when no exit works for a service, get a WARP exit a new
	// IP (a fresh account), at most RotateHourly times an hour.
	AutoRotate   bool `json:"auto_rotate"`
	RotateHourly int  `json:"rotate_hourly"`
	// Other is the exit for traffic that isn't an AI service: "" = the
	// server's normal route.
	Other string `json:"other,omitempty"`
}

type Panel struct {
	Port int    `json:"port"`
	Path string `json:"path"` // secret URL path, without slashes
	User string `json:"user"`
	Salt string `json:"salt"`
	Hash string `json:"hash"` // sha256(salt + password), hex
	Key  string `json:"key"`  // signs session cookies
}

type Proxy struct {
	Listen string `json:"listen"` // "127.0.0.1" or "0.0.0.0"
	Port   int    `json:"port"`
	User   string `json:"user,omitempty"`
	Pass   string `json:"pass,omitempty"`
}

type Exit struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Address, User, Pass: for socks5 and http exits.
	Address string `json:"address,omitempty"`
	User    string `json:"user,omitempty"`
	Pass    string `json:"pass,omitempty"`
	// Account: for warp exits, filled in on first start.
	Account *WARPAccount `json:"account,omitempty"`
	// License is an optional WARP+ key.
	License string `json:"license,omitempty"`
	// Region: Opera's region (EU, AS, AM) or Psiphon's exit country
	// ("" = Psiphon picks).
	Region string `json:"region,omitempty"`
	// Key is an SSH private key (PEM); HostKey the server's key, learned
	// on the first connection and required to match from then on.
	Key     string `json:"key,omitempty"`
	HostKey string `json:"host_key,omitempty"`
}

type WARPAccount struct {
	ID         string `json:"id"`
	Token      string `json:"token"`
	PrivateKey string `json:"private_key"` // base64
	PeerKey    string `json:"peer_key"`    // base64
	Endpoint   string `json:"endpoint"`    // host:port
	V4         string `json:"v4"`
	V6         string `json:"v6"`
	Plus       bool   `json:"plus,omitempty"`
}

type Service struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Pin     string   `json:"pin,omitempty"` // an exit ID; "" = pick automatically
	Domains []string `json:"domains"`
	Custom  bool     `json:"custom,omitempty"` // added in the panel; can be deleted
}

// DefaultServices is the list a new install starts with.
func DefaultServices() []Service {
	return []Service{
		{ID: "chatgpt", Name: "ChatGPT", Enabled: true, Domains: []string{
			"openai.com", "chatgpt.com", "chat.com", "oaistatic.com", "oaiusercontent.com", "sora.com", "openai.org", "chatgpt.livekit.cloud", "host.livekit.cloud", "turn.livekit.cloud",
		}},
		{ID: "claude", Name: "Claude", Enabled: true, Domains: []string{
			"claude.ai", "anthropic.com", "claude.com", "claudeusercontent.com", "clau.de",
		}},
		{ID: "gemini", Name: "Gemini", Enabled: true, Domains: []string{
			"gemini.google.com", "aistudio.google.com", "generativelanguage.googleapis.com", "ai.google.dev", "makersuite.google.com",
			"alkalimakersuite-pa.clients6.google.com", "alkalicore-pa.clients6.google.com", "notebooklm.google.com", "notebooklm.google", "labs.google", "deepmind.google", "geminiapi.google.com", "proactivebackend-pa.googleapis.com", "robinfrontend-pa.googleapis.com",
		}},
		{ID: "copilot", Name: "Copilot", Enabled: false, Domains: []string{
			"copilot.microsoft.com", "sydney.bing.com", "edgeservices.bing.com", "githubcopilot.com",
		}},
		{ID: "perplexity", Name: "Perplexity", Enabled: false, Domains: []string{
			"perplexity.ai", "pplx.ai",
		}},
		{ID: "grok", Name: "Grok", Enabled: false, Domains: []string{
			"grok.com", "x.ai",
		}},
		// Not AI, but asked for: YouTube's test is whether Premium (no ads)
		// is sold from the exit's country; Instagram's is plain access.
		{ID: "youtube", Name: "YouTube", Enabled: false, Domains: []string{
			"youtube.com", "youtu.be", "googlevideo.com", "ytimg.com", "ggpht.com", "youtubei.googleapis.com", "youtube-nocookie.com",
		}},
		{ID: "instagram", Name: "Instagram", Enabled: false, Domains: []string{
			"instagram.com", "cdninstagram.com", "ig.me", "fbcdn.net",
		}},
	}
}

// Store keeps the configuration and writes it back on every change.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func Open(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.fill()
	return &Store{path: path, cfg: c}, nil
}

// Create writes a fresh configuration and returns it with the panel
// password, which is shown once and only its hash is kept.
func Create(path string) (*Store, string, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, "", errors.New("already set up: " + path)
	}
	pass := RandomString(14)
	c := Config{
		Panel: Panel{Port: FreePort(20000, 60000), Path: RandomString(10), User: "admin", Key: RandomString(32)},
		Proxy: Proxy{Listen: "127.0.0.1", Port: FreePort(20000, 40000)},
		Exits: []Exit{
			{ID: "warp", Kind: WARP, Name: "WARP", Enabled: true},
			{ID: "ipv4", Kind: Direct4, Name: "IPv4 سرور", Enabled: true},
			{ID: "ipv6", Kind: Direct6, Name: "IPv6 سرور", Enabled: true},
		},
		Services:     DefaultServices(),
		CheckMinutes: 10,
		SameCountry:  true,
		AutoRotate:   true,
		RotateHourly: 4,
	}
	c.Panel.SetPassword(pass)
	s := &Store{path: path, cfg: c}
	return s, pass, s.save()
}

func (c *Config) fill() {
	if c.CheckMinutes <= 0 {
		c.CheckMinutes = 10
	}
	if c.RotateHourly <= 0 {
		c.RotateHourly = 4
	}
	if c.Proxy.Listen == "" {
		c.Proxy.Listen = "127.0.0.1"
	}
	// Services added in newer versions show up switched off.
	have := map[string]bool{}
	for _, s := range c.Services {
		have[s.ID] = true
	}
	for _, s := range DefaultServices() {
		if !have[s.ID] {
			s.Enabled = false
			c.Services = append(c.Services, s)
		}
	}
}

// Get returns a copy of the configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.clone()
}

// Update changes the configuration under the lock and saves it.
func (s *Store) Update(f func(c *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg.clone()
	if err := f(&c); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	s.cfg = c
	return s.save()
}

func (s *Store) save() error {
	b, _ := json.MarshalIndent(s.cfg, "", "  ")
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (c Config) clone() Config {
	b, _ := json.Marshal(c)
	var d Config
	json.Unmarshal(b, &d)
	return d
}

func (c *Config) Validate() error {
	ids := map[string]bool{}
	for _, e := range c.Exits {
		if e.ID == "" || ids[e.ID] {
			return fmt.Errorf("exit id %q is empty or used twice", e.ID)
		}
		ids[e.ID] = true
		switch e.Kind {
		case Direct4, Direct6, WARP, Psiphon:
		case Opera:
			if e.Region != "EU" && e.Region != "AS" && e.Region != "AM" {
				return fmt.Errorf("%s: Opera's region is EU, AS or AM", e.Name)
			}
		case SOCKS5, HTTP, SSH:
			if _, _, err := net.SplitHostPort(e.Address); err != nil {
				return fmt.Errorf("%s: address must be host:port", e.Name)
			}
			if e.Kind == SSH && (e.User == "" || (e.Pass == "" && e.Key == "")) {
				return fmt.Errorf("%s: SSH needs a username and a password or key", e.Name)
			}
		default:
			return fmt.Errorf("unknown exit kind %q", e.Kind)
		}
	}
	for _, s := range c.Services {
		if s.Pin != "" && !ids[s.Pin] {
			return fmt.Errorf("%s is pinned to an exit that no longer exists", s.Name)
		}
	}
	if c.Other != "" && !ids[c.Other] {
		return errors.New("the exit for other traffic no longer exists")
	}
	if c.Proxy.Port < 1 || c.Proxy.Port > 65535 || c.Panel.Port < 1 || c.Panel.Port > 65535 {
		return errors.New("ports must be 1-65535")
	}
	if c.Proxy.Port == c.Panel.Port {
		return errors.New("the proxy and the panel need different ports")
	}
	if c.Proxy.Listen != "127.0.0.1" && c.Proxy.User == "" {
		return errors.New("a proxy open to the internet needs a username and password")
	}
	return nil
}

// Exit returns the exit with this ID.
func (c *Config) Exit(id string) *Exit {
	for i := range c.Exits {
		if c.Exits[i].ID == id {
			return &c.Exits[i]
		}
	}
	return nil
}

// Service returns the service with this ID.
func (c *Config) Service(id string) *Service {
	for i := range c.Services {
		if c.Services[i].ID == id {
			return &c.Services[i]
		}
	}
	return nil
}

// NewServiceID returns an unused service ID made from name.
func (c *Config) NewServiceID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	base := b.String()
	if base == "" {
		base = "service"
	}
	if len(base) > 20 {
		base = base[:20]
	}
	id := base
	for n := 2; c.Service(id) != nil; n++ {
		id = fmt.Sprintf("%s%d", base, n)
	}
	return id
}

// NormalizeDomains turns what people paste (URLs, "domain:x", "*.x",
// one per line or comma-separated) into a clean list of domains.
func NormalizeDomains(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		for _, d := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '،' || unicode.IsSpace(r) }) {
			d = strings.ToLower(strings.TrimSpace(d))
			d = strings.TrimPrefix(d, "domain:")
			if i := strings.Index(d, "://"); i >= 0 {
				d = d[i+3:]
			}
			d, _, _ = strings.Cut(d, "/")
			if h, _, err := net.SplitHostPort(d); err == nil {
				d = h
			}
			d = strings.TrimLeft(d, "*.")
			d = strings.TrimSuffix(d, ".")
			if d == "" {
				continue
			}
			if !strings.Contains(d, ".") || strings.ContainsAny(d, "\"'<>@ ") {
				return nil, fmt.Errorf("دامنه‌ی «%s» درست نیست", d)
			}
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("دست‌کم یک دامنه لازم است")
	}
	if len(out) > 300 {
		return nil, errors.New("حداکثر ۳۰۰ دامنه")
	}
	return out, nil
}

// NewExitID returns an ID not used yet, starting from base.
func (c *Config) NewExitID(base string) string {
	id := base
	for n := 2; c.Exit(id) != nil; n++ {
		id = fmt.Sprintf("%s%d", base, n)
	}
	return id
}

func (p *Panel) SetPassword(pass string) {
	p.Salt = RandomString(16)
	h := sha256.Sum256([]byte(p.Salt + pass))
	p.Hash = hex.EncodeToString(h[:])
}

func (p Panel) CheckPassword(user, pass string) bool {
	h := sha256.Sum256([]byte(p.Salt + pass))
	okUser := subtle.ConstantTimeCompare([]byte(user), []byte(p.User)) == 1
	okPass := subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(p.Hash)) == 1
	return okUser && okPass
}

const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"

func RandomString(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b.WriteByte(alphabet[k.Int64()])
	}
	return b.String()
}

// FreePort picks a random TCP port in [lo, hi) that nothing listens on.
func FreePort(lo, hi int) int {
	for i := 0; i < 50; i++ {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(hi-lo)))
		p := lo + int(k.Int64())
		if l, err := net.Listen("tcp", fmt.Sprintf(":%d", p)); err == nil {
			l.Close()
			return p
		}
	}
	return lo
}
