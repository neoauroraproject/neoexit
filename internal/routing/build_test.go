package routing

import (
	"net"
	"testing"
)

// Written exactly as in 3x-ui's routing rules.
func TestBuildXraySyntax(t *testing.T) {
	m, err := Build([]string{"myip.ms", "ext:hmray.dat:game-ea", "ext:hmray.dat:game-riot", "ext:hmray.dat:game-epic", "ext:hmray.dat:youtube"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"myip.ms": true, "www.myip.ms": true, "www.youtube.com": true, "googlevideo.com": true,
		"example.com": false,
	} {
		if got := m.MatchHost(host); got != want {
			t.Errorf("%s: %v", host, got)
		}
	}
	d, _ := Build([]string{"domain:example.com", "full:a.b.com", "keyword:tracker", `regexp:\.ir$`}, nil)
	for host, want := range map[string]bool{
		"example.com": true, "x.example.com": true, "notexample.com": false,
		"a.b.com": true, "x.a.b.com": false, "my-tracker.org": true, "site.ir": true, "ir.com": false,
	} {
		if got := d.MatchHost(host); got != want {
			t.Errorf("%s: %v", host, got)
		}
	}
	ip, err := Build(nil, []string{"geoip:private", "1.2.3.0/24", "9.9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{"192.168.1.1": true, "1.2.3.7": true, "9.9.9.9": true, "9.9.9.8": false} {
		if got := ip.MatchIP(net.ParseIP(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
	// geosite:/geoip: fall back to the HMray lists without geosite.dat.
	GeoDirs = func() []string { return []string{t.TempDir()} }
	if _, err := Build([]string{"geosite:telegram"}, []string{"geoip:telegram"}); err != nil {
		t.Error(err)
	}
}
