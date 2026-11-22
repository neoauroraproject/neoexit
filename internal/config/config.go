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
	"slices"
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
	Panel Panel `json:"panel"`
	Proxy Proxy `json:"proxy"`
	// Extra are more SOCKS5/HTTP ports, each with its own login and exit.
	Extra    []ExtraProxy `json:"extra_proxies,omitempty"`
	Exits    []Exit       `json:"exits"`    // in priority order
	Services []Service    `json:"services"` // the AI services checked and routed

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
	// RoutingFiles: the Smart Routing lists were put in 3x-ui's folder from
	// the panel; keep them there and current after every update.
	RoutingFiles bool `json:"routing_files,omitempty"`
	// Routing is the routing table, checked in order after the AI
	// services; what matches no rule goes to Other.
	Routing []Rule `json:"routing,omitempty"`
	// PrivateDirect lets proxies reach the server's own networks
	// (127.0.0.1, 10.x...; 3x-ui's "geoip:private"); off, they are
	// refused, but for AllowPrivate.
	PrivateDirect bool     `json:"private_direct,omitempty"`
	AllowPrivate  []string `json:"allow_private,omitempty"`
	DNS           DNS      `json:"dns"`
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
	// Exit: all of this port's traffic through one exit (and nothing
	// another way while it is down); "" = the routing table.
	Exit string `json:"exit,omitempty"`
}

// Rule is one row of the routing table, written like a 3x-ui (Xray)
// routing rule: every condition given must hold; inside one list, any
// item will do. Checked in order; the first that matches decides.
type Rule struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// Domains, as in Xray: "x" (anywhere in the name), "domain:x" (x and
	// below), "full:x", "regexp:x", "geosite:x" (geosite.dat, else
	// hmray.dat), "ext:file.dat:x" (hmray.dat built in; other files from
	// /etc/neoexit/geo or 3x-ui's folder).
	Domains []string `json:"domains,omitempty"`
	// IPs: "1.2.3.4", "1.2.3.0/24", "geoip:x" (geoip.dat, else
	// hmray-geoip.dat; "geoip:private" built in), "ext:file.dat:x".
	IPs []string `json:"ips,omitempty"`
	// Ports: "443", "1000-2000", "80,443".
	Ports   string `json:"ports,omitempty"`
	Network string `json:"network,omitempty"` // "", "tcp", "udp"
	// Protocols found in the first bytes: http, tls, quic, bittorrent.
	Protocols []string `json:"protocols,omitempty"`
	// Inbounds: the NeoExit proxies it applies to ("main" or an extra
	// proxy's ID); none = all. Like Xray's inboundTag.
	Inbounds []string `json:"inbounds,omitempty"`
	Target   string   `json:"target"` // an exit ID, "direct" or "block"
}

// Protocols a rule can name.
var Protocols = []string{"http", "tls", "quic", "bittorrent"}

// DNS is how NeoExit resolves names for traffic it sends out itself (the
// server's route and its own IPv4/IPv6): Iranian names through Iranian
// servers, so they get Iranian addresses; the rest through Servers.
type DNS struct {
	// IROff turns the Iranian DNS off (all names through Servers).
	IROff bool `json:"ir_off,omitempty"`
	// IRServers: empty = DefaultIRServers.
	IRServers []string `json:"ir_servers,omitempty"`
	// IRDomains, written like a rule's domains: empty = DefaultIRDomains.
	IRDomains []string `json:"ir_domains,omitempty"`
	// Servers for every other name: empty = the server's own DNS.
	Servers []string `json:"servers,omitempty"`
}

var (
	DefaultIRServers = []string{"178.22.122.100", "185.51.200.2", "217.218.127.127"}
	DefaultIRDomains = []string{"domain:ir", "ext:hmray.dat:iran"}
)

// ExtraProxy is one more SOCKS5/HTTP port with its own login; it follows
// the routing table (rules may name it) or sends everything to Exit.
type ExtraProxy struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Listen string `json:"listen"` // "127.0.0.1" or "0.0.0.0"
	Port   int    `json:"port"`
	User   string `json:"user,omitempty"`
	Pass   string `json:"pass,omitempty"`
	Exit   string `json:"exit,omitempty"`
}

// Targets of routing rules besides exit IDs.
const (
	TargetDirect = "direct" // the server's own route
	TargetBlock  = "block"  // refuse the connection
)

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
	ports := map[int]string{c.Panel.Port: "the panel", c.Proxy.Port: "the main proxy"}
	pids := map[string]bool{}
	for _, x := range c.Extra {
		if x.ID == "" || pids[x.ID] {
			return fmt.Errorf("proxy id %q is empty or used twice", x.ID)
		}
		pids[x.ID] = true
		if x.Port < 1 || x.Port > 65535 {
			return fmt.Errorf("%s: ports must be 1-65535", x.Name)
		}
		if used, ok := ports[x.Port]; ok {
			return fmt.Errorf("%s: port %d is already used by %s", x.Name, x.Port, used)
		}
		ports[x.Port] = x.Name
		if x.Listen != "127.0.0.1" && x.Listen != "0.0.0.0" {
			return fmt.Errorf("%s: bad listen address", x.Name)
		}
		if x.Listen != "127.0.0.1" && x.User == "" {
			return fmt.Errorf("%s: a proxy open to the internet needs a username and password", x.Name)
		}
		if x.Exit != "" && !ids[x.Exit] {
			return fmt.Errorf("%s uses an exit that no longer exists", x.Name)
		}
	}
	if c.Proxy.Exit != "" && !ids[c.Proxy.Exit] {
		return errors.New("the main proxy uses an exit that no longer exists")
	}
	if err := c.checkRouting(ids, pids); err != nil {
		return err
	}
	for _, list := range [][]string{c.DNS.IRServers, c.DNS.Servers} {
		for _, s := range list {
			if h, _, err := net.SplitHostPort(s); (err != nil || net.ParseIP(h) == nil) && net.ParseIP(s) == nil {
				return fmt.Errorf("DNS server %q is not an IP (or IP:port)", s)
			}
		}
	}
	for _, a := range c.AllowPrivate {
		if _, _, err := net.ParseCIDR(a); err != nil && net.ParseIP(a) == nil {
			return fmt.Errorf("%q is not an IP or CIDR", a)
		}
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

func (c *Config) checkRouting(ids, proxies map[string]bool) error {
	target := func(t string) bool { return t == TargetDirect || t == TargetBlock || ids[t] }
	if len(c.Routing) > 500 {
		return errors.New("at most 500 rules")
	}
	seen := map[string]bool{}
	for i, x := range c.Routing {
		n := i + 1
		if x.ID == "" || seen[x.ID] {
			return fmt.Errorf("rule %d: id empty or used twice", n)
		}
		seen[x.ID] = true
		if !target(x.Target) {
			return fmt.Errorf("قانون %d به خروجی‌ای می‌رود که دیگر نیست", n)
		}
		if x.Network != "" && x.Network != "tcp" && x.Network != "udp" {
			return fmt.Errorf("قانون %d: شبکه tcp یا udp است", n)
		}
		for _, p := range x.Protocols {
			if !slices.Contains(Protocols, p) {
				return fmt.Errorf("قانون %d: پروتکل %q شناخته نیست", n, p)
			}
		}
		for _, in := range x.Inbounds {
			if in != "main" && !proxies[in] {
				return fmt.Errorf("قانون %d: پروکسی %q دیگر نیست", n, in)
			}
		}
		if len(x.Domains) == 0 && len(x.IPs) == 0 && x.Ports == "" && x.Network == "" && len(x.Protocols) == 0 && len(x.Inbounds) == 0 {
			return fmt.Errorf("قانون %d هیچ شرطی ندارد", n)
		}
	}
	return nil
}

// UsesExit says whether any proxy or rule sends traffic to exit id, and
// names it.
func (c *Config) UsesExit(id string) string {
	if c.Proxy.Exit == id {
		return "پروکسی اصلی"
	}
	for _, x := range c.Extra {
		if x.Exit == id {
			return "پروکسی «" + x.Name + "»"
		}
	}
	for i, x := range c.Routing {
		if x.Target == id {
			return fmt.Sprintf("قانون %d روتینگ", i+1)
		}
	}
	return ""
}

// NewRuleID returns an unused rule ID.
func (c *Config) NewRuleID() string {
	for {
		id := "r" + RandomString(6)
		used := false
		for _, x := range c.Routing {
			used = used || x.ID == id
		}
		if !used {
			return id
		}
	}
}

// ExtraProxy returns the extra proxy with this ID.
func (c *Config) ExtraProxy(id string) *ExtraProxy {
	for i := range c.Extra {
		if c.Extra[i].ID == id {
			return &c.Extra[i]
		}
	}
	return nil
}

// NewExtraID returns an unused extra proxy ID.
func (c *Config) NewExtraID() string {
	for n := 1; ; n++ {
		if id := fmt.Sprintf("p%d", n); c.ExtraProxy(id) == nil {
			return id
		}
	}
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
