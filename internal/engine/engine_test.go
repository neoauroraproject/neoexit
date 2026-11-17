package engine

import "testing"

func TestMatches(t *testing.T) {
	d := []string{"openai.com", "chatgpt.com"}
	for host, want := range map[string]bool{
		"openai.com": true, "api.openai.com": true, "chatgpt.com": true,
		"notopenai.com": false, "openai.com.evil.net": false, "google.com": false,
	} {
		if got := matches(host, d); got != want {
			t.Errorf("matches(%q) = %v, want %v", host, got, want)
		}
	}
}
