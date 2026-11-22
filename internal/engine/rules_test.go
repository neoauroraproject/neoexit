package engine

import (
	"path/filepath"
	"testing"

	"neoexit/internal/config"
	"neoexit/internal/netguard"
	"neoexit/internal/route"
)

func testEngine(t *testing.T, edit func(c *config.Config)) *Engine {
	t.Helper()
	dir := t.TempDir()
	st, _, err := config.Create(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		if err := st.Update(func(c *config.Config) error { edit(c); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	e := New(st, dir)
	// Pretend the exits are up, without starting them.
	e.mu.Lock()
	for _, id := range []string{"warp", "ipv4", "ipv6"} {
		e.dialers[id] = blockDialer{}
	}
	e.mu.Unlock()
	return e
}

func TestDecide(t *testing.T) {
	netguard.SetAllow(nil)
	netguard.SetDirect(false)
	e := testEngine(t, func(c *config.Config) {
		c.Extra = []config.ExtraProxy{
			{ID: "p1", Name: "games", Listen: "127.0.0.1", Port: 30001},
			{ID: "p2", Name: "v4", Listen: "127.0.0.1", Port: 30002, Exit: "ipv4"},
		}
		c.Routing = []config.Rule{
			{ID: "ads", Domains: []string{"ext:hmray.dat:ads"}, Target: "block"},
			{ID: "bt", Protocols: []string{"bittorrent"}, Target: "block"},
			// the user's 3x-ui rule, as written there
			{ID: "games", Domains: []string{"myip.ms", "ext:hmray.dat:game-ea", "ext:hmray.dat:game-riot", "ext:hmray.dat:game-epic", "ext:hmray.dat:youtube"}, Inbounds: []string{"p1"}, Target: "warp"},
			// domains and IPs in separate rules: in one rule both must hold
			{ID: "iran", Domains: []string{"domain:ir", "ext:hmray.dat:iran"}, Target: "direct"},
			{ID: "iran-ip", IPs: []string{"ext:hmray-geoip.dat:iran"}, Target: "direct"},
			{ID: "udp", Ports: "6881-6889", Network: "udp", Target: "block"},
			{ID: "net", IPs: []string{"203.0.113.0/24"}, Target: "ipv6"},
			{ID: "google", Domains: []string{"google.com"}, Target: "ipv4"},
		}
		c.Other = "warp"
	})
	cases := []struct {
		name string
		req  route.Request
		want route.Decision
	}{
		{"loopback", route.Request{Host: "127.0.0.1", Port: 80, Network: "tcp"}, route.Decision{Target: "block", Rule: "private"}},
		{"private udp", route.Request{Host: "10.1.2.3", Port: 53, Network: "udp"}, route.Decision{Target: "block", Rule: "private"}},
		{"fixed proxy", route.Request{Host: "digikala.com", Port: 443, Network: "tcp", Proxy: "p2"}, route.Decision{Target: "ipv4", Rule: "exit"}},
		{"ads", route.Request{Host: "doubleclick.net", Port: 443, Network: "tcp"}, route.Decision{Target: "block", Rule: "rule:ads"}},
		{"torrent", route.Request{Host: "8.8.8.8", Port: 51413, Network: "tcp", Protocol: "bittorrent"}, route.Decision{Target: "block", Rule: "rule:bt"}},
		{"games on p1", route.Request{Host: "www.myip.ms", Port: 443, Network: "tcp", Proxy: "p1"}, route.Decision{Target: "warp", Rule: "rule:games"}},
		{"youtube on p1", route.Request{Host: "www.youtube.com", Port: 443, Network: "tcp", Proxy: "p1"}, route.Decision{Target: "warp", Rule: "rule:games"}},
		{"games rule not on main", route.Request{Host: "www.myip.ms", Port: 443, Network: "tcp", Proxy: "main"}, route.Decision{Target: "warp", Rule: "default"}},
		{"iran by name", route.Request{Host: "www.digikala.com", Port: 443, Network: "tcp"}, route.Decision{Target: "direct", Rule: "rule:iran"}},
		{"iran .ir", route.Request{Host: "x.y.ir", Port: 443, Network: "tcp"}, route.Decision{Target: "direct", Rule: "rule:iran"}},
		{"iran by IP", route.Request{Host: "2.144.0.1", Port: 443, Network: "tcp"}, route.Decision{Target: "direct", Rule: "rule:iran-ip"}},
		{"both must hold", route.Request{Host: "x.y.ir", Port: 443, Network: "tcp"}, route.Decision{Target: "direct", Rule: "rule:iran"}},
		{"sniffed name", route.Request{Host: "140.82.112.3", Port: 443, Network: "tcp", Sniffed: "www.digikala.com"}, route.Decision{Target: "direct", Rule: "rule:iran"}},
		{"udp ports", route.Request{Host: "1.1.1.1", Port: 6881, Network: "udp"}, route.Decision{Target: "block", Rule: "rule:udp"}},
		{"udp rule, tcp", route.Request{Host: "1.1.1.1", Port: 6881, Network: "tcp"}, route.Decision{Target: "warp", Rule: "default"}},
		{"cidr", route.Request{Host: "203.0.113.9", Port: 443, Network: "tcp"}, route.Decision{Target: "ipv6", Rule: "rule:net"}},
		{"keyword", route.Request{Host: "mail.google.com", Port: 443, Network: "tcp"}, route.Decision{Target: "ipv4", Rule: "rule:google"}},
		{"rest", route.Request{Host: "example.org", Port: 443, Network: "tcp"}, route.Decision{Target: "warp", Rule: "default"}},
	}
	for _, c := range cases {
		if got := e.Decide(c.req); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// AI comes before the table: a rule can't send ChatGPT elsewhere.
func TestAIFirst(t *testing.T) {
	e := testEngine(t, func(c *config.Config) {
		c.Routing = []config.Rule{{ID: "all", Domains: []string{"com"}, Target: "block"}}
	})
	e.mu.Lock()
	e.services["chatgpt"] = &ServiceState{Exit: "warp"}
	e.mu.Unlock()
	if got := e.Decide(route.Request{Host: "chatgpt.com", Port: 443, Network: "tcp"}); got != (route.Decision{Target: "warp", Rule: "service:chatgpt"}) {
		t.Errorf("chatgpt: %+v", got)
	}
	// An AI site that is no service goes where the AI services go.
	if got := e.Decide(route.Request{Host: "huggingface.co", Port: 443, Network: "tcp"}); got.Rule != "service:ai" || got.Target != "warp" {
		t.Errorf("other AI site: %+v", got)
	}
	if got := e.Decide(route.Request{Host: "example.com", Port: 443, Network: "tcp"}); got.Rule != "rule:all" {
		t.Errorf("plain site: %+v", got)
	}
}

func TestPrivateDirect(t *testing.T) {
	e := testEngine(t, nil)
	netguard.SetDirect(true)
	defer netguard.SetDirect(false)
	if got := e.Decide(route.Request{Host: "127.0.0.1", Port: 80, Network: "tcp"}); got != (route.Decision{Target: "direct", Rule: "private"}) {
		t.Errorf("%+v", got)
	}
}

func TestCompileErrors(t *testing.T) {
	for _, r := range []config.Rule{
		{ID: "a", Domains: []string{"regexp:("}, Target: "direct"},
		{ID: "a", IPs: []string{"300.1.1.1"}, Target: "direct"},
		{ID: "a", Domains: []string{"ext:hmray.dat:no-such-list"}, Target: "direct"},
		{ID: "a", Domains: []string{"ext:../etc/passwd:x"}, Target: "direct"},
		{ID: "a", Ports: "70000", Target: "direct"},
		{ID: "a", Ports: "90-80", Target: "direct"},
	} {
		if _, err := CompileTable([]config.Rule{r}); err == nil {
			t.Errorf("%+v compiled", r)
		}
	}
	// A disabled rule is not even compiled.
	if _, err := CompileTable([]config.Rule{{ID: "a", Disabled: true, Domains: []string{"regexp:("}, Target: "direct"}}); err != nil {
		t.Error(err)
	}
}

func TestStats(t *testing.T) {
	e := testEngine(t, nil)
	e.Count(route.Decision{Target: "direct", Rule: "default"}, 100)
	e.Count(route.Decision{Target: "direct", Rule: "default"}, 50)
	s := e.Stats()
	if s["default"] != (Counter{Conns: 2, Bytes: 150}) || s["target:direct"].Bytes != 150 {
		t.Fatalf("%+v", s)
	}
}
