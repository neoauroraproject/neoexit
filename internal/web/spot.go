package web

// Sponsored messages in the panel, from the NeoAurora ads service (the
// same one NeoTunnel uses). The panel gets them from this server's own
// address, so the browser never talks to an ads host. Nothing about this
// server is sent: only the product and where the message is shown.
//
// The ads service counts a view for every GET /v1/ads that returns an ad,
// so this server makes exactly one such request for each time the panel
// shows one. It keeps one ad ready per place: a view shows it at once and
// asks for the next in the background (that request is the view's count).
// Only when nothing is ready, or what is ready has gone stale, does a view
// wait for its own request.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
	"unicode/utf8"
)

var spotBase = "https://license.hossein-m466.workers.dev/v1/ads" // a var for tests

const (
	spotProduct = "neoexit"
	// While NeoExit has no messages of its own, it shows the shared
	// NeoTunnel ones (views and clicks are counted for the product they
	// came from).
	spotFallback = "neotunnel"

	spotStale = time.Hour        // a ready ad older than this is asked for again
	spotEmpty = 10 * time.Minute // after "no ads", before asking again
	spotRetry = 2 * time.Minute  // after a failed request
	spotOwed  = 50               // most views waiting to be counted, per place
	// One browser counts at most once per place in this window. The login
	// page shows a message too, so anyone could reload it to inflate views.
	spotPerIP = 2 * time.Minute
)

// Spot is one sponsored message as the panel shows it.
type Spot struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Button  string `json:"button,omitempty"`
	Link    string `json:"link,omitempty"`
	Badge   string `json:"badge,omitempty"`
	product string // the product it was served for
	place   string // the placement it was served for
}

// "home" is the dashboard's card, "sidebar" the line on every other page.
var spotPlaces = map[string]bool{"home": true, "sidebar": true}

type spotSlot struct {
	ad      *Spot
	at      time.Time // when ad was fetched (or "none" was learned)
	wait    time.Time // no requests before this (after a failure or "none")
	owed    int       // views shown that still need their request
	running bool
}

type spots struct {
	once  sync.Once
	mu    sync.Mutex
	slots map[string]*spotSlot
	seen  map[string]time.Time // ip|place -> last counted view
	shown map[string]Spot      // recently shown, by id, so clicks are known
	order []string             // ids in shown, oldest first
}

var spotClient = &http.Client{Timeout: 8 * time.Second}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// fetchOne asks for one ad; the ads service counts a view when it returns one.
func fetchOne(ctx context.Context, product, place string) (*Spot, error) {
	q := url.Values{"product": {product}, "placement": {place}, "mode": {"rotate"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, spotBase+"?"+q.Encode(), nil)
	res, err := spotClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var r struct {
		Ad *struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Body       string `json:"body"`
			ButtonText string `json:"buttonText"`
			LinkURL    string `json:"linkUrl"`
			Badge      string `json:"badge"`
		} `json:"ad"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&r); err != nil {
		return nil, err
	}
	if r.Ad == nil || r.Ad.ID == "" || r.Ad.Title == "" {
		return nil, nil
	}
	s := &Spot{ID: clip(r.Ad.ID, 64), Title: clip(r.Ad.Title, 80), Body: clip(r.Ad.Body, 240), Button: clip(r.Ad.ButtonText, 30), Badge: clip(r.Ad.Badge, 20), product: product, place: place}
	// Only plain web links open from the panel.
	if u, err := url.Parse(r.Ad.LinkURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		s.Link = u.String()
	}
	return s, nil
}

// fetch tries NeoExit's own ads, then the shared ones, then (for the
// sidebar) the dashboard's. Answers without an ad count nothing, so only
// the one that is shown is counted.
func fetch(place string) (*Spot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tries := [][2]string{{spotProduct, place}, {spotFallback, place}}
	if place != "home" {
		tries = append(tries, [2]string{spotProduct, "home"}, [2]string{spotFallback, "home"})
	}
	for _, t := range tries {
		s, err := fetchOne(ctx, t[0], t[1])
		if err != nil || s != nil {
			return s, err
		}
	}
	return nil, nil
}

func (s *spots) start() { s.once.Do(s.init) }

func (s *spots) init() {
	s.slots = map[string]*spotSlot{}
	for p := range spotPlaces {
		s.slots[p] = &spotSlot{}
	}
	s.seen = map[string]time.Time{}
	s.shown = map[string]Spot{}
	// Have one ready for the first view (this request is its count).
	s.mu.Lock()
	for p, sl := range s.slots {
		s.owe(sl, p, 1)
	}
	s.mu.Unlock()
}

// store records a request's result in the slot; the caller holds s.mu.
func (s *spots) store(sl *spotSlot, ad *Spot, err error) {
	now := time.Now()
	switch {
	case err != nil:
		sl.wait = now.Add(spotRetry) // keep what is ready
	case ad == nil:
		sl.ad, sl.at, sl.wait = nil, now, now.Add(spotEmpty)
	default:
		sl.ad, sl.at, sl.wait = ad, now, time.Time{}
	}
}

// owe adds n requests to make for the slot and starts making them; the
// caller holds s.mu.
func (s *spots) owe(sl *spotSlot, place string, n int) {
	sl.owed += n
	if sl.owed > spotOwed {
		sl.owed = spotOwed
	}
	if !sl.running && sl.owed > 0 {
		sl.running = true
		go s.fill(sl, place)
	}
}

// fill makes the owed requests one after another, each one the count for
// a view. After a failure or "no ads" the rest are dropped: nothing can be
// counted for them then.
func (s *spots) fill(sl *spotSlot, place string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sl.owed > 0 {
		if time.Now().Before(sl.wait) {
			sl.owed = 0
			break
		}
		sl.owed--
		s.mu.Unlock()
		ad, err := fetch(place)
		s.mu.Lock()
		s.store(sl, ad, err)
		if err != nil || ad == nil {
			sl.owed = 0
		}
	}
	sl.running = false
}

// view gives the ad to show for one panel view and has it counted.
func (s *spots) view(place, ip string) *Spot {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sl := s.slots[place]
	count := true
	if ip != "" {
		k := ip + "|" + place
		if t, ok := s.seen[k]; ok && now.Sub(t) < spotPerIP {
			count = false
		} else {
			s.seen[k] = now
			if len(s.seen) > 5000 {
				for k, t := range s.seen {
					if now.Sub(t) >= spotPerIP {
						delete(s.seen, k)
					}
				}
			}
		}
	}
	ready := sl.ad != nil && now.Sub(sl.at) < spotStale
	if !ready && !sl.running && now.After(sl.wait) {
		// Nothing fresh to show: this view waits for its own request,
		// which is also its count. The ad then stays ready: the next view
		// shows it again and makes the next request.
		sl.running = true
		s.mu.Unlock()
		ad, err := fetch(place)
		s.mu.Lock()
		s.store(sl, ad, err)
		sl.running = false
		if err != nil || sl.ad == nil {
			sl.owed = 0
		}
		s.owe(sl, place, 0) // views that came in meanwhile
		s.remember(sl.ad)
		return sl.ad
	}
	ad := sl.ad
	s.remember(ad)
	if ad != nil && count {
		s.owe(sl, place, 1)
	}
	return ad
}

// remember keeps the last shown ads so their clicks can be passed on; the
// caller holds s.mu.
func (s *spots) remember(ad *Spot) {
	if ad == nil {
		return
	}
	if _, ok := s.shown[ad.ID]; !ok {
		s.order = append(s.order, ad.ID)
		if len(s.order) > 100 {
			delete(s.shown, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.shown[ad.ID] = *ad
}

func (s *spots) find(id string) *Spot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp, ok := s.shown[id]; ok {
		return &sp
	}
	return nil
}

// GET api/spot?p=home|sidebar. Open (the login page shows one too): it
// holds only the public message text.
func (a *App) spot(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("p")
	if !spotPlaces[p] {
		p = "sidebar"
	}
	writeJSON(w, map[string]any{"spot": a.spots.view(p, clientIP(r))})
}

// POST api/spot/open: a click, passed on to the ads service under the
// product and placement the ad was served for, so views and clicks match.
func (a *App) spotOpen(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
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
	body, _ := json.Marshal(map[string]string{"adId": sp.ID, "productSlug": sp.product, "placement": sp.place})
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
