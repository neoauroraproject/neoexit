package web

// Sponsored messages in the panel, from the NeoAurora ads service (the
// same one NeoTunnel uses). This server fetches them every two hours and
// keeps them; the panel gets them from its own address, so the browser
// never talks to an ads host. Nothing about this server is sent: only the
// product and where the message is shown.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	spotBase    = "https://license.hossein-m466.workers.dev/v1/ads"
	spotProduct = "neoexit"
	// While NeoExit has no messages of its own, it shows the shared
	// NeoTunnel ones (clicks are counted for the product they came from).
	spotFallback = "neotunnel"
	spotEvery    = 2 * time.Hour
	spotRetry    = 5 * time.Minute // after a failed fetch
)

// Spot is one sponsored message as the panel shows it.
type Spot struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Button  string `json:"button,omitempty"`
	Link    string `json:"link,omitempty"`
	Badge   string `json:"badge,omitempty"`
	product string
}

// "home" is the dashboard's card, "sidebar" the line on every other page.
var spotPlaces = []string{"home", "sidebar"}

type spots struct {
	once sync.Once
	mu   sync.RWMutex
	list map[string][]Spot
	turn atomic.Uint64
}

var spotClient = &http.Client{Timeout: 15 * time.Second}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func fetchSpots(ctx context.Context, product, place string) ([]Spot, error) {
	q := url.Values{"product": {product}, "placement": {place}, "mode": {"rotate"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, spotBase+"?"+q.Encode(), nil)
	res, err := spotClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	type raw struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		ButtonText string `json:"buttonText"`
		LinkURL    string `json:"linkUrl"`
		Badge      string `json:"badge"`
	}
	var r struct {
		Ads []raw `json:"ads"`
		Ad  *raw  `json:"ad"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&r); err != nil {
		return nil, err
	}
	if len(r.Ads) == 0 && r.Ad != nil {
		r.Ads = []raw{*r.Ad}
	}
	var out []Spot
	for _, a := range r.Ads {
		if a.ID == "" || a.Title == "" {
			continue
		}
		s := Spot{ID: clip(a.ID, 64), Title: clip(a.Title, 80), Body: clip(a.Body, 240), Button: clip(a.ButtonText, 30), Badge: clip(a.Badge, 20), product: product}
		// Only plain web links open from the panel.
		if u, err := url.Parse(a.LinkURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			s.Link = u.String()
		}
		out = append(out, s)
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}

// refresh fetches every placement once; it reports whether all worked.
// A placement that fails keeps what it had.
func (s *spots) refresh() bool {
	ok := true
	for _, place := range spotPlaces {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		list, err := fetchSpots(ctx, spotProduct, place)
		if err == nil && len(list) == 0 {
			list, err = fetchSpots(ctx, spotFallback, place)
		}
		cancel()
		if err != nil {
			ok = false
			continue
		}
		s.mu.Lock()
		s.list[place] = list
		s.mu.Unlock()
	}
	return ok
}

func (s *spots) start() {
	s.once.Do(func() {
		s.list = map[string][]Spot{}
		go func() {
			for {
				wait := spotEvery
				if !s.refresh() {
					wait = spotRetry
				}
				time.Sleep(wait)
			}
		}()
	})
}

// next gives the placement's messages in turn; "sidebar" borrows the
// dashboard's when it has none.
func (s *spots) next(place string) *Spot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l := s.list[place]
	if len(l) == 0 {
		l = s.list["home"]
	}
	if len(l) == 0 {
		return nil
	}
	sp := l[int(s.turn.Add(1)%uint64(len(l)))]
	return &sp
}

func (s *spots) find(id string) *Spot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, l := range s.list {
		for _, sp := range l {
			if sp.ID == id {
				return &sp
			}
		}
	}
	return nil
}

// GET api/spot?p=home|sidebar. Open (the login page shows one too): it
// holds only the public message text.
func (a *App) spot(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	if p != "home" {
		p = "sidebar"
	}
	writeJSON(w, map[string]any{"spot": a.spots.next(p)})
}

// POST api/spot/open: a click, passed on to the ads service.
func (a *App) spotOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string `json:"id"`
		Placement string `json:"placement"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.ID == "" {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	sp := a.spots.find(in.ID)
	if sp == nil {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if in.Placement != "home" {
		in.Placement = "sidebar"
	}
	body, _ := json.Marshal(map[string]string{"adId": sp.ID, "productSlug": sp.product, "placement": in.Placement})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, spotBase+"/click", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if res, err := spotClient.Do(req); err == nil {
			res.Body.Close()
		}
	}()
	writeJSON(w, map[string]bool{"ok": true})
}
