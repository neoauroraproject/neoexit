package netguard

import (
	"net"
	"testing"
)

func TestBlocked(t *testing.T) {
	SetAllow(nil)
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "10.2.3.4": true, "172.20.0.1": true, "192.168.1.1": true, "169.254.169.254": true,
		"100.64.0.1": true, "0.0.0.0": true, "::1": true, "fd00::1": true, "fe80::1": true, "::ffff:127.0.0.1": true,
		"1.1.1.1": false, "8.8.8.8": false, "172.32.0.1": false, "2606:4700::1111": false, "::ffff:1.1.1.1": false,
	} {
		if got := Blocked(net.ParseIP(ip)); got != want {
			t.Errorf("%s: %v", ip, got)
		}
	}
	SetAllow([]string{"10.0.0.0/8", "192.168.1.5"})
	defer SetAllow(nil)
	if Blocked(net.ParseIP("10.9.9.9")) || Blocked(net.ParseIP("192.168.1.5")) || !Blocked(net.ParseIP("192.168.1.6")) {
		t.Error("allow list")
	}
	if Control("tcp", "127.0.0.1:80", nil) == nil || Control("tcp", "1.1.1.1:443", nil) != nil {
		t.Error("Control")
	}
}
