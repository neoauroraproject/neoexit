// Package check tests one exit: where it leaves from (IP, country) and
// whether each AI service lets that address in.
//
// A service answers in one of four ways, and they need different cures:
//
//	ok         the service works from here
//	geo        "not available in your country": move to another country
//	challenge  a captcha or bot wall against this IP: a new IP is enough
//	error      the exit itself didn't connect
package check

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	OK        = "ok"
	Geo       = "geo"
	Challenge = "challenge"
	Error     = "error"
)

// Probe is one request and how to read its answer. The built-in list can
// be replaced by /etc/neoexit/probes.json when a service changes its
// block page, without a new release.
type Probe struct {
	URL    string `json:"url"`
	Follow bool   `json:"follow"` // follow redirects
	// Geo / Challenge: the answer means that state if any rule matches.
	Geo       []Rule `json:"geo"`
	Challenge []Rule `json:"challenge"`
}

// Rule matches when all its non-empty fields match.
type Rule struct {
	Status   int    `json:"status,omitempty"`
	Body     string `json:"body,omitempty"`     // substring of the body, any case
	Location string `json:"location,omitempty"` // substring of the redirect or final URL
	Header   string `json:"header,omitempty"`   // "Name: value-substring"
}

// cfChallenge: a hard Cloudflare block on this IP. Cloudflare's "Just a
// moment" page is not one: a real browser passes it on its own.
var cfChallenge = []Rule{{Body: "you have been blocked"}, {Body: "error code: 1020"}, {Body: "access denied | "}}

// Builtin probes per service ID.
var Builtin = map[string][]Probe{
	"chatgpt": {{
		URL:       "https://api.openai.com/v1/models",
		Geo:       []Rule{{Body: "unsupported_country"}},
		Challenge: cfChallenge,
	}, {
		URL:       "https://chatgpt.com/cdn-cgi/trace",
		Challenge: cfChallenge,
	}},
	"claude": {{
		URL:       "https://claude.ai/login",
		Geo:       []Rule{{Location: "app-unavailable-in-region"}},
		Challenge: cfChallenge,
	}, {
		URL: "https://api.anthropic.com/v1/models",
		Geo: []Rule{{Status: 403, Body: "request not allowed"}, {Status: 403, Body: "unsupported_country"}},
	}},
	"gemini": {{
		URL:       "https://aistudio.google.com/",
		Follow:    true,
		Geo:       []Rule{{Location: "available-regions"}, {Body: "is not available in your country"}, {Body: "not supported in your country"}},
		Challenge: []Rule{{Location: "google.com/sorry"}, {Status: 429}},
	}, {
		URL:       "https://gemini.google.com/app",
		Follow:    true,
		Geo:       []Rule{{Body: "not supported in your country"}, {Body: "isn't supported in your country"}, {Location: "available-regions"}},
		Challenge: []Rule{{Location: "google.com/sorry"}, {Status: 429}},
	}},
	"copilot": {{
		URL:       "https://copilot.microsoft.com/",
		Follow:    true,
		Geo:       []Rule{{Body: "not available in your region"}, {Body: "isn't available in your region"}},
		Challenge: cfChallenge,
	}},
	"perplexity": {{
		URL:       "https://www.perplexity.ai/",
		Follow:    true,
		Geo:       []Rule{{Body: "not available in your region"}},
		Challenge: cfChallenge,
	}},
	"grok": {{
		URL:       "https://grok.com/",
		Follow:    true,
		Geo:       []Rule{{Body: "not available in your region"}, {Body: "not available in your country"}},
		Challenge: cfChallenge,
	}},
	// "geo" here means YouTube Premium (the only reliable way to get no
	// ads) is not sold in the exit's country; YouTube itself still plays.
	"youtube": {{
		URL:       "https://www.youtube.com/premium",
		Follow:    true,
		Geo:       []Rule{{Body: "premium is not available in your country"}, {Body: "not available in your country"}, {Location: "google.cn"}},
		Challenge: []Rule{{Location: "google.com/sorry"}, {Status: 429}},
	}},
	"instagram": {{
		URL:       "https://www.instagram.com/",
		Follow:    true,
		Geo:       []Rule{{Body: "not available in your country"}},
		Challenge: []Rule{{Status: 429}, {Location: "/challenge/"}},
	}},
}

// Generic is the test for a service added in the panel: its first domain
// must open, and must not say it's unavailable in this country.
func Generic(domains []string) []Probe {
	if len(domains) == 0 {
		return nil
	}
	return []Probe{{
		URL:    "https://" + domains[0] + "/",
		Follow: true,
		Geo: []Rule{{Body: "not available in your country"}, {Body: "not available in your region"},
			{Body: "isn't available in your country"}, {Body: "isn't available in your region"}, {Status: 451}},
		Challenge: append([]Rule{{Location: "google.com/sorry"}, {Status: 429}}, cfChallenge...),
	}}
}

// LoadProbes returns the built-in probes, replaced per service by any in
// path.
func LoadProbes(path string) map[string][]Probe {
	out := map[string][]Probe{}
	for k, v := range Builtin {
		out[k] = v
	}
	if b, err := os.ReadFile(path); err == nil {
		var over map[string][]Probe
		if json.Unmarshal(b, &over) == nil {
			for k, v := range over {
				out[k] = v
			}
		}
	}
	return out
}

type Dial func(ctx context.Context, network, addr string) (net.Conn, error)

func client(dial Dial, follow bool) *http.Client {
	tr := &http.Transport{
		DialContext:           dial,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
	}
	c := &http.Client{Transport: tr, Timeout: 25 * time.Second}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func get(ctx context.Context, c *http.Client, url string) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	return resp, string(b), nil
}

// Trace is where an exit leaves from, as Cloudflare sees it.
type Trace struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Colo    string `json:"colo"`
	WARP    string `json:"warp,omitempty"` // "on", "plus" or "off"
	Ms      int    `json:"ms"`
}

func TraceExit(ctx context.Context, dial Dial) (Trace, error) {
	var t Trace
	start := time.Now()
	resp, body, err := get(ctx, client(dial, false), "https://www.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return t, shortErr(err)
	}
	if resp.StatusCode != 200 {
		return t, errors.New("trace: " + resp.Status)
	}
	t.Ms = int(time.Since(start).Milliseconds())
	for _, line := range strings.Split(body, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "ip":
			t.IP = v
		case "loc":
			t.Country = v
		case "colo":
			t.Colo = v
		case "warp":
			t.WARP = v
		}
	}
	return t, nil
}

// Result is one service through one exit.
type Result struct {
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
	Ms     int       `json:"ms"`
	At     time.Time `json:"at"`
}

// Service runs a service's probes through dial. The worst answer wins: a
// geo block on any probe means the service won't work from here.
func Service(ctx context.Context, dial Dial, probes []Probe) Result {
	r := Result{State: OK, At: time.Now()}
	start := time.Now()
	if len(probes) == 0 {
		r.State, r.Detail = Error, "no test for this service"
		return r
	}
	errs := 0
	for _, p := range probes {
		state, detail := run(ctx, dial, p)
		switch state {
		case Geo:
			r.State, r.Detail = Geo, detail
		case Challenge:
			if r.State != Geo {
				r.State, r.Detail = Challenge, detail
			}
		case Error:
			errs++
			if r.Detail == "" {
				r.Detail = detail
			}
		}
	}
	if errs == len(probes) {
		r.State = Error
	}
	r.Ms = int(time.Since(start).Milliseconds())
	return r
}

func run(ctx context.Context, dial Dial, p Probe) (string, string) {
	resp, body, err := get(ctx, client(dial, p.Follow), p.URL)
	if err != nil {
		return Error, shortErr(err).Error()
	}
	loc := resp.Header.Get("Location")
	if resp.Request != nil && resp.Request.URL != nil {
		loc += " " + resp.Request.URL.String()
	}
	low := strings.ToLower(body)
	for _, r := range p.Geo {
		if r.match(resp, low, loc) {
			return Geo, describe(p.URL, resp)
		}
	}
	for _, r := range p.Challenge {
		if r.match(resp, low, loc) {
			return Challenge, describe(p.URL, resp)
		}
	}
	if resp.StatusCode >= 500 {
		return Error, describe(p.URL, resp)
	}
	return OK, ""
}

func (r Rule) match(resp *http.Response, lowBody, loc string) bool {
	if r.Status == 0 && r.Body == "" && r.Location == "" && r.Header == "" {
		return false
	}
	if r.Status != 0 && resp.StatusCode != r.Status {
		return false
	}
	if r.Body != "" && !strings.Contains(lowBody, strings.ToLower(r.Body)) {
		return false
	}
	if r.Location != "" && !strings.Contains(loc, r.Location) {
		return false
	}
	if r.Header != "" {
		name, val, _ := strings.Cut(r.Header, ":")
		if !strings.Contains(strings.ToLower(resp.Header.Get(strings.TrimSpace(name))), strings.ToLower(strings.TrimSpace(val))) {
			return false
		}
	}
	return true
}

func describe(url string, resp *http.Response) string {
	host := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	return host + " → " + resp.Status
}

func shortErr(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("timed out")
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && len(s) > 120 {
		s = s[i+2:]
	}
	return errors.New(s)
}
