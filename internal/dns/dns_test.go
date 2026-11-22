package dns

import (
	"testing"

	"neoexit/internal/config"
)

func TestIranianNames(t *testing.T) {
	if err := Set(config.DNS{}); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{"digikala.com": true, "www.aparat.com": true, "x.y.ir": true, "google.com": false} {
		if got := IsIranian(host); got != want {
			t.Errorf("%s: %v", host, got)
		}
	}
	Set(config.DNS{IROff: true})
	if IsIranian("x.ir") {
		t.Error("off, still Iranian")
	}
	if err := Set(config.DNS{IRDomains: []string{"regexp:("}}); err == nil {
		t.Error("bad list passed")
	}
	if IsIranian("x.ir") {
		t.Error("a bad list must leave the Iranian DNS off")
	}
}
