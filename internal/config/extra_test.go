package config

import "testing"

func TestExtraProxyValidate(t *testing.T) {
	base := func() Config {
		return Config{
			Panel: Panel{Port: 1000},
			Proxy: Proxy{Listen: "127.0.0.1", Port: 1001},
			Exits: []Exit{{ID: "warp", Kind: WARP, Name: "WARP"}},
		}
	}
	cases := []struct {
		name string
		x    ExtraProxy
		ok   bool
	}{
		{"smart", ExtraProxy{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 2000}, true},
		{"fixed", ExtraProxy{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 2000, Exit: "warp"}, true},
		{"open with login", ExtraProxy{ID: "p1", Name: "a", Listen: "0.0.0.0", Port: 2000, User: "u", Pass: "p"}, true},
		{"open without login", ExtraProxy{ID: "p1", Name: "a", Listen: "0.0.0.0", Port: 2000}, false},
		{"main proxy's port", ExtraProxy{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 1001}, false},
		{"panel's port", ExtraProxy{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 1000}, false},
		{"missing exit", ExtraProxy{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 2000, Exit: "gone"}, false},
		{"bad listen", ExtraProxy{ID: "p1", Name: "a", Listen: "1.2.3.4", Port: 2000}, false},
	}
	for _, c := range cases {
		cfg := base()
		cfg.Extra = []ExtraProxy{c.x}
		if err := cfg.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
	cfg := base()
	cfg.Extra = []ExtraProxy{{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 2000}, {ID: "p2", Name: "b", Listen: "127.0.0.1", Port: 2000}}
	if cfg.Validate() == nil {
		t.Error("two extra proxies on one port passed")
	}
}

func TestRoutingValidate(t *testing.T) {
	cfg := Config{Panel: Panel{Port: 1000}, Proxy: Proxy{Listen: "127.0.0.1", Port: 1001},
		Exits: []Exit{{ID: "warp", Kind: WARP, Name: "WARP"}},
		Extra: []ExtraProxy{{ID: "p1", Name: "a", Listen: "127.0.0.1", Port: 2000}}}
	good := []Rule{
		{ID: "a", Domains: []string{"myip.ms", "ext:hmray.dat:game-ea"}, Target: "warp"},
		{ID: "a", Ports: "6881-6889", Network: "udp", Target: "block"},
		{ID: "a", Protocols: []string{"bittorrent", "quic"}, Target: "block"},
		{ID: "a", Inbounds: []string{"main", "p1"}, Target: "direct"},
	}
	for _, r := range good {
		cfg.Routing = []Rule{r}
		if err := cfg.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Rule{
		{ID: "a", Target: "direct"}, // no condition
		{ID: "", Ports: "80", Target: "direct"},
		{ID: "a", Domains: []string{"x.com"}, Target: "gone"},
		{ID: "a", Ports: "80", Network: "icmp", Target: "direct"},
		{ID: "a", Protocols: []string{"ftp"}, Target: "direct"},
		{ID: "a", Inbounds: []string{"p9"}, Target: "direct"},
	}
	for _, r := range bad {
		cfg.Routing = []Rule{r}
		if cfg.Validate() == nil {
			t.Errorf("%+v passed", r)
		}
	}
	cfg.Routing = []Rule{{ID: "a", Ports: "1", Target: "direct"}, {ID: "a", Ports: "2", Target: "direct"}}
	if cfg.Validate() == nil {
		t.Error("two rules with one id passed")
	}
	cfg.Routing = nil
	cfg.DNS = DNS{IRServers: []string{"178.22.122.100", "1.1.1.1:5353"}}
	if err := cfg.Validate(); err != nil {
		t.Error(err)
	}
	cfg.DNS = DNS{Servers: []string{"dns.google"}}
	if cfg.Validate() == nil {
		t.Error("a DNS server by name passed")
	}
	cfg.DNS = DNS{}
	cfg.Routing = []Rule{{ID: "a", Ports: "1", Target: "warp"}}
	if cfg.UsesExit("warp") == "" || cfg.UsesExit("ipv4") != "" {
		t.Error("UsesExit")
	}
}
