package routing

import (
	"net"
	"strings"
	"testing"
)

func TestCategoriesExist(t *testing.T) {
	sites, ips := Codes()
	has := func(list []string, s string) bool {
		for _, x := range list {
			if x == strings.ToUpper(s) {
				return true
			}
		}
		return false
	}
	for _, c := range Categories {
		for _, s := range c.Sites {
			if !has(sites, s) {
				t.Errorf("%s: hmray.dat has no %q", c.ID, s)
			}
		}
		for _, s := range c.IPs {
			if !has(ips, s) {
				t.Errorf("%s: hmray-geoip.dat has no %q", c.ID, s)
			}
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		cat, host string
		ip        string
		want      bool
	}{
		{"iran", "digikala.com", "", true},
		{"iran", "www.aparat.com", "", true},
		{"iran", "anything.ir", "", true},
		{"iran", "google.com", "", false},
		{"messaging", "web.telegram.org", "", true},
		{"messaging", "", "149.154.167.50", true},
		{"developer", "github.com", "", true},
		{"streaming", "www.youtube.com", "", true},
		{"block", "doubleclick.net", "", true},
		{"block", "github.com", "", false},
	}
	for _, c := range cases {
		m, err := For(c.cat)
		if err != nil {
			t.Fatal(err)
		}
		got := false
		if c.host != "" {
			got = m.MatchHost(c.host)
		} else {
			got = m.MatchIP(net.ParseIP(c.ip))
		}
		if got != c.want {
			t.Errorf("%s %s%s: got %v", c.cat, c.host, c.ip, got)
		}
	}
}
