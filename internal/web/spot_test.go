package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeAds counts views the way the ads service does: one per GET that
// returns an ad.
type fakeAds struct {
	mu    sync.Mutex
	views map[string]int // product|placement
	n     int
	none  map[string]bool // product|placement with no ads
}

func (f *fakeAds) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := r.URL.Query().Get("product") + "|" + r.URL.Query().Get("placement")
	if f.none[k] {
		json.NewEncoder(w).Encode(map[string]any{"ad": nil})
		return
	}
	f.n++
	f.views[k]++
	json.NewEncoder(w).Encode(map[string]any{"ad": map[string]string{"id": "ad" + strconv.Itoa(f.n), "title": "t"}})
}

func (f *fakeAds) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := 0
	for _, v := range f.views {
		t += v
	}
	return t
}

func settle(s *spots) {
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		busy := false
		for _, sl := range s.slots {
			busy = busy || sl.running || sl.owed > 0
		}
		s.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newFake(t *testing.T, none ...string) (*fakeAds, *spots) {
	f := &fakeAds{views: map[string]int{}, none: map[string]bool{}}
	for _, k := range none {
		f.none[k] = true
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := spotBase
	spotBase = srv.URL
	t.Cleanup(func() { spotBase = old })
	s := &spots{}
	s.start()
	settle(s)
	return f, s
}

func TestEachViewCountsOnce(t *testing.T) {
	f, s := newFake(t)
	// Start-up readied one per place: those are the first views' counts.
	if got := f.total(); got != 2 {
		t.Fatalf("after start: %d requests, want 2", got)
	}
	for i := 0; i < 30; i++ {
		if s.view("home", "10.0.0."+strconv.Itoa(i)) == nil {
			t.Fatal("no ad shown")
		}
		settle(s)
	}
	// 30 home views: the ready one plus one per view (the last is ready
	// for view 31), and the sidebar's ready one.
	if got := f.views["neoexit|home"]; got != 31 {
		t.Fatalf("home: %d counted, want 31", got)
	}
}

func TestSameBrowserCountsOncePerWindow(t *testing.T) {
	f, s := newFake(t)
	for i := 0; i < 10; i++ {
		s.view("sidebar", "1.2.3.4")
		settle(s)
	}
	if got := f.views["neoexit|sidebar"]; got != 2 {
		t.Fatalf("sidebar: %d counted, want 2 (ready + one view)", got)
	}
}

func TestFallbackAndClickPlacement(t *testing.T) {
	f, s := newFake(t, "neoexit|sidebar", "neotunnel|sidebar", "neoexit|home")
	a := s.view("sidebar", "1.1.1.1")
	settle(s)
	if a == nil || a.product != "neotunnel" || a.place != "home" {
		t.Fatalf("sidebar fell back to %+v, want neotunnel/home", a)
	}
	if got := s.find(a.ID); got == nil || got.place != "home" {
		t.Fatal("shown ad not found for its click")
	}
	if f.views["neotunnel|home"] != 3 { // home ready, sidebar ready (from home), this view
		t.Fatalf("neotunnel|home: %d, want 3", f.views["neotunnel|home"])
	}
}

func TestConcurrentViewsAllCounted(t *testing.T) {
	f, s := newFake(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.view("home", "ip"+strconv.Itoa(i))
		}(i)
	}
	wg.Wait()
	settle(s)
	if got := f.views["neoexit|home"]; got != 41 {
		t.Fatalf("home: %d counted, want 41", got)
	}
}
