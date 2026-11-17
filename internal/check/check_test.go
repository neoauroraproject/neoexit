package check

import (
	"net/http"
	"testing"
)

func TestRules(t *testing.T) {
	resp := &http.Response{StatusCode: 403, Header: http.Header{}}
	geo := Rule{Body: "unsupported_country"}
	if !geo.match(resp, `{"error":{"code":"unsupported_country_region_territory"}}`, "") {
		t.Fatal("OpenAI's country error should match")
	}
	if geo.match(resp, `{"error":"invalid_api_key"}`, "") {
		t.Fatal("a key error is not a country block")
	}
	loc := Rule{Location: "app-unavailable-in-region"}
	if !loc.match(&http.Response{StatusCode: 307}, "", "https://www.anthropic.com/app-unavailable-in-region") {
		t.Fatal("Claude's redirect should match")
	}
	if (Rule{}).match(resp, "anything", "") {
		t.Fatal("an empty rule must never match")
	}
}
