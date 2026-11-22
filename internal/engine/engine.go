// Package engine ties it together: it keeps a dialer per exit, tests
// every exit against every service on a timer, picks the exit each
// service uses, gets WARP a new IP when nothing works, and remembers what
// it did and why.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"neoexit/internal/check"
	"neoexit/internal/config"
	"neoexit/internal/exit"
	"neoexit/internal/warp"
)

type ExitState struct {
	ID        string                  `json:"id"`
	Trace     check.Trace             `json:"trace"`
	Up        bool                    `json:"up"`
	Err       string                  `json:"err,omitempty"`
	Results   map[string]check.Result `json:"results"` // by service ID
	CheckedAt time.Time               `json:"checked_at"`
	Checking  bool                    `json:"checking"`
}

type ServiceState struct {
	ID     string    `json:"id"`
	Exit   string    `json:"exit"`   // the exit it uses now ("" = none works)
	State  string    `json:"state"`  // that exit's result for it
	Since  time.Time `json:"since"`  // on this exit since
	Reason string    `json:"reason"` // why this exit
}

type Event struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // switch, rotate, down, info, error
	Text string    `json:"text"`
}

type Engine struct {
	store  *config.Store
	dir    string
	probes map[string][]check.Probe

	mu        sync.Mutex
	dialers   map[string]exit.Dialer
	built     map[string]string // exit ID -> config it was built from
	building  map[string]string // slow exits starting in the background
	exits     map[string]*ExitState
	services  map[string]*ServiceState
	events    []Event
	rotations []time.Time
	lastRun   time.Time
	running   bool

	kick chan struct{}

	statsMu sync.Mutex
	stats   map[string]*Counter // rule or "target:<id>" -> counts

	table atomic.Pointer[Table] // the routing table in use
}

func New(store *config.Store, dir string) *Engine {
	e := &Engine{
		stats:    map[string]*Counter{},
		store:    store,
		dir:      dir,
		probes:   check.LoadProbes(filepath.Join(dir, "probes.json")),
		dialers:  map[string]exit.Dialer{},
		built:    map[string]string{},
		building: map[string]string{},
		exits:    map[string]*ExitState{},
		services: map[string]*ServiceState{},
		kick:     make(chan struct{}, 1),
	}
	e.load()
	e.SetRouting()
	return e
}

// Run tests now and then every CheckMinutes, until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	e.Reload()
	for {
		e.checkAll(ctx)
		wait := time.Duration(e.store.Get().CheckMinutes) * time.Minute
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-e.kick:
		}
	}
}

// Close stops every exit (Psiphon's process with it).
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, d := range e.dialers {
		d.Close()
		delete(e.dialers, id)
	}
	e.building = map[string]string{}
}

// CheckNow starts a test round unless one is running.
func (e *Engine) CheckNow() {
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// Reload brings the dialers in line with the configuration: new exits get
// one, changed ones are rebuilt, removed ones closed.
func (e *Engine) Reload() {
	cfg := e.store.Get()
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := map[string]bool{}
	for _, x := range cfg.Exits {
		keep[x.ID] = true
		if e.exits[x.ID] == nil {
			e.exits[x.ID] = &ExitState{ID: x.ID, Results: map[string]check.Result{}}
		}
		sig := sig(x)
		if e.built[x.ID] == sig && (e.dialers[x.ID] != nil || e.building[x.ID] == sig) {
			continue
		}
		if d := e.dialers[x.ID]; d != nil {
			if x.Kind == config.Psiphon {
				go d.Close() // two clients can't share one data folder
			} else {
				go closeLater(d)
			}
			delete(e.dialers, x.ID)
		}
		delete(e.built, x.ID)
		delete(e.building, x.ID)
		if !x.Enabled || (x.Kind == config.WARP && x.Account == nil) {
			continue
		}
		if exit.Slow(x.Kind) {
			// Needs the network or a download: build it in the background
			// and test it as soon as it's there.
			e.built[x.ID] = sig
			e.building[x.ID] = sig
			st := e.exits[x.ID]
			st.Up, st.Err, st.Results = false, "در حال راه‌اندازی…", map[string]check.Result{}
			go e.build(x, sig)
			continue
		}
		d, err := exit.New(context.Background(), x, e.options(x.ID))
		if err != nil {
			e.exits[x.ID].Err = err.Error()
			e.exits[x.ID].Up = false
			continue
		}
		e.dialers[x.ID] = d
		e.built[x.ID] = sig
	}
	for id, d := range e.dialers {
		if !keep[id] {
			go closeLater(d)
			delete(e.dialers, id)
			delete(e.built, id)
		}
	}
	for id := range e.exits {
		if !keep[id] {
			delete(e.exits, id)
			delete(e.building, id)
		}
	}
	e.choose(cfg, true)
}

func sig(x config.Exit) string {
	// A new name or a learned host key doesn't make it a different exit:
	// renaming must not restart Psiphon or register Opera again.
	x.HostKey, x.Name = "", ""
	b, _ := json.Marshal(x)
	return string(b)
}

func (e *Engine) options(id string) exit.Options {
	return exit.Options{
		DataDir: e.dir,
		OnHostKey: func(key string) {
			e.store.Update(func(c *config.Config) error {
				if x := c.Exit(id); x != nil && x.HostKey == "" {
					x.HostKey = key
				}
				return nil
			})
		},
	}
}

// build starts a slow exit, then tests it.
func (e *Engine) build(x config.Exit, sig string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	d, err := exit.New(ctx, x, e.options(x.ID))
	if err == nil {
		if w, ok := d.(interface{ WaitReady(context.Context) bool }); ok {
			wctx, wcancel := context.WithTimeout(ctx, 90*time.Second)
			w.WaitReady(wctx)
			wcancel()
		}
	}
	e.mu.Lock()
	if e.building[x.ID] != sig {
		e.mu.Unlock()
		if d != nil {
			d.Close()
		}
		return
	}
	delete(e.building, x.ID)
	st := e.exits[x.ID]
	if err != nil {
		delete(e.built, x.ID)
		if st != nil {
			st.Up, st.Err, st.CheckedAt = false, err.Error(), time.Now()
		}
		e.eventLocked("error", x.Name+": "+err.Error())
		e.mu.Unlock()
		return
	}
	e.dialers[x.ID] = d
	if st != nil {
		st.Err = ""
	}
	e.mu.Unlock()
	cfg := e.store.Get()
	if cur := cfg.Exit(x.ID); cur != nil {
		e.checkExit(context.Background(), cfg, *cur)
		e.mu.Lock()
		e.chooseLocked(cfg)
		e.mu.Unlock()
		e.save()
	}
}

// closeLater gives open connections a moment before the old dialer goes.
func closeLater(d exit.Dialer) {
	time.Sleep(90 * time.Second)
	d.Close()
}

func (e *Engine) checkAll(ctx context.Context) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.lastRun = time.Now()
		e.mu.Unlock()
		e.save()
	}()

	e.ensureWARP(ctx)
	cfg := e.store.Get()
	var wg sync.WaitGroup
	for _, x := range cfg.Exits {
		if !x.Enabled {
			continue
		}
		wg.Add(1)
		go func(x config.Exit) {
			defer wg.Done()
			e.checkExit(ctx, cfg, x)
		}(x)
	}
	wg.Wait()

	e.mu.Lock()
	e.chooseLocked(cfg)
	stuck := e.stuckLocked(cfg)
	e.mu.Unlock()
	if len(stuck) > 0 && cfg.AutoRotate {
		e.autoRotate(ctx, cfg, stuck)
	}
}

// ensureWARP registers an account for every WARP exit that has none.
func (e *Engine) ensureWARP(ctx context.Context) {
	cfg := e.store.Get()
	for _, x := range cfg.Exits {
		if x.Kind != config.WARP || !x.Enabled || x.Account != nil {
			continue
		}
		acc, err := warp.Register(ctx, x.License)
		if acc == nil {
			e.event("error", fmt.Sprintf("%s: ساخت حساب WARP نشد: %v", x.Name, err))
			e.setErr(x.ID, err)
			continue
		}
		if err != nil {
			e.event("error", fmt.Sprintf("%s: %v", x.Name, err))
		}
		e.store.Update(func(c *config.Config) error {
			if ex := c.Exit(x.ID); ex != nil {
				ex.Account = acc
			}
			return nil
		})
		e.event("info", x.Name+": حساب WARP ساخته شد")
	}
	e.Reload()
}

func (e *Engine) setErr(id string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s := e.exits[id]; s != nil {
		s.Up, s.Err, s.CheckedAt = false, err.Error(), time.Now()
	}
}

func (e *Engine) checkExit(ctx context.Context, cfg config.Config, x config.Exit) {
	e.mu.Lock()
	d := e.dialers[x.ID]
	st := e.exits[x.ID]
	if st != nil {
		st.Checking = true
	}
	e.mu.Unlock()
	if st == nil {
		return
	}
	defer func() {
		e.mu.Lock()
		st.Checking = false
		e.mu.Unlock()
	}()
	if d == nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	tr, err := check.TraceExit(tctx, d.DialContext)
	cancel()
	results := map[string]check.Result{}
	if err == nil {
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, s := range cfg.Services {
			if !s.Enabled {
				continue
			}
			wg.Add(1)
			go func(s config.Service) {
				defer wg.Done()
				sctx, cancel := context.WithTimeout(ctx, 40*time.Second)
				probes := e.probes[s.ID]
				if len(probes) == 0 {
					probes = check.Generic(s.Domains)
				}
				r := check.Service(sctx, d.DialContext, probes)
				cancel()
				mu.Lock()
				results[s.ID] = r
				mu.Unlock()
			}(s)
		}
		wg.Wait()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	st.CheckedAt = time.Now()
	if err != nil {
		st.Up, st.Err = false, friendly(x.Kind, err)
		st.Results = map[string]check.Result{}
		return
	}
	st.Up, st.Err, st.Trace, st.Results = true, "", tr, results
}

// chooseLocked picks the exit for each service. A service stays where it
// is while that still works (moving makes accounts look suspicious); when
// it must move, it goes to the first working exit in priority order,
// preferring the country it was in if SameCountry is on.
func (e *Engine) chooseLocked(cfg config.Config) { e.choose(cfg, false) }

// choose: quiet skips history entries (a reload between test rounds).
func (e *Engine) choose(cfg config.Config, quiet bool) {
	for _, s := range cfg.Services {
		cur := e.services[s.ID]
		if cur == nil {
			cur = &ServiceState{ID: s.ID}
			e.services[s.ID] = cur
		}
		if !s.Enabled {
			cur.Exit, cur.State, cur.Reason = "", "", "خاموش"
			continue
		}
		if s.Pin != "" {
			e.setRoute(cfg, s, cur, s.Pin, "ثابت روی این خروجی")
			continue
		}
		works := func(id string) bool {
			x := cfg.Exit(id)
			st := e.exits[id]
			return x != nil && x.Enabled && st != nil && st.Up && e.dialers[id] != nil && st.Results[s.ID].State == check.OK
		}
		if cur.Exit != "" && works(cur.Exit) {
			cur.State = check.OK
			continue
		}
		prevCountry := ""
		if cur.Exit != "" && e.exits[cur.Exit] != nil {
			prevCountry = e.exits[cur.Exit].Trace.Country
		}
		pick := ""
		if cfg.SameCountry && prevCountry != "" {
			for _, x := range cfg.Exits {
				if works(x.ID) && e.exits[x.ID].Trace.Country == prevCountry {
					pick = x.ID
					break
				}
			}
		}
		if pick == "" {
			for _, x := range cfg.Exits {
				if works(x.ID) {
					pick = x.ID
					break
				}
			}
		}
		if pick == "" {
			if !quiet && cur.State != "down" && cur.Exit != "" {
				e.eventLocked("down", fmt.Sprintf("%s: هیچ خروجی‌ای جواب نمی‌دهد", s.Name))
			}
			if cur.Exit == "" || cfg.Exit(cur.Exit) == nil {
				// Nothing works and nothing was chosen: keep traffic on the
				// first enabled exit rather than nowhere.
				for _, x := range cfg.Exits {
					if x.Enabled {
						cur.Exit = x.ID
						break
					}
				}
			}
			cur.State, cur.Reason = "down", "هیچ خروجی‌ای قبول نشد"
			continue
		}
		reason := "اولین خروجی سالم"
		if cfg.SameCountry && prevCountry != "" && e.exits[pick].Trace.Country == prevCountry {
			reason = "سالم، در همان کشور قبلی"
		}
		if !quiet && cur.Exit != "" && cur.Exit != pick {
			why := describeState(e.exits[cur.Exit], s.ID)
			e.eventLocked("switch", fmt.Sprintf("%s از %s به %s رفت، چون %s", s.Name, exitName(cfg, cur.Exit), exitName(cfg, pick), why))
		}
		e.setRoute(cfg, s, cur, pick, reason)
	}
}

func (e *Engine) setRoute(cfg config.Config, s config.Service, cur *ServiceState, id, reason string) {
	if cur.Exit != id {
		cur.Since = time.Now()
	}
	cur.Exit, cur.Reason = id, reason
	if st := e.exits[id]; st != nil {
		cur.State = st.Results[s.ID].State
	}
}

func describeState(st *ExitState, svc string) string {
	if st == nil {
		return "آن خروجی حذف شد"
	}
	if !st.Up {
		return "خود خروجی وصل نشد"
	}
	switch st.Results[svc].State {
	case check.Geo:
		return "کشورش را قبول نکرد"
	case check.Challenge:
		return "IP آن کپچا گرفت"
	case check.Error:
		return "تست به خطا خورد"
	}
	return "خاموش شد"
}

func exitName(cfg config.Config, id string) string {
	if x := cfg.Exit(id); x != nil {
		return x.Name
	}
	return id
}

// stuckLocked lists enabled services no exit works for.
func (e *Engine) stuckLocked(cfg config.Config) []string {
	var out []string
	for _, s := range cfg.Services {
		if st := e.services[s.ID]; s.Enabled && s.Pin == "" && st != nil && st.State == "down" {
			out = append(out, s.ID)
		}
	}
	return out
}

// autoRotate gets each WARP exit a new IP (within the hourly budget) and
// tests again, until a stuck service works or the budget runs out.
func (e *Engine) autoRotate(ctx context.Context, cfg config.Config, stuck []string) {
	for _, x := range cfg.Exits {
		if x.Kind != config.WARP || !x.Enabled {
			continue
		}
		for tries := 0; tries < 2; tries++ {
			if !e.takeRotation(cfg.RotateHourly) {
				return
			}
			if err := e.rotate(ctx, x.ID, "خودکار"); err != nil {
				return
			}
			fresh := e.store.Get()
			e.checkExit(ctx, fresh, *fresh.Exit(x.ID))
			e.mu.Lock()
			e.chooseLocked(fresh)
			left := e.stuckLocked(fresh)
			e.mu.Unlock()
			if len(left) == 0 {
				return
			}
		}
	}
}

func (e *Engine) takeRotation(hourly int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	cut := time.Now().Add(-time.Hour)
	e.rotations = slices.DeleteFunc(e.rotations, func(t time.Time) bool { return t.Before(cut) })
	if len(e.rotations) >= hourly {
		return false
	}
	e.rotations = append(e.rotations, time.Now())
	return true
}

// Rotate gives a WARP exit a new account (and so usually a new IP), then
// tests it.
func (e *Engine) Rotate(ctx context.Context, id string) error {
	if err := e.rotate(ctx, id, "دستی"); err != nil {
		return err
	}
	cfg := e.store.Get()
	if x := cfg.Exit(id); x != nil {
		e.checkExit(ctx, cfg, *x)
	}
	e.mu.Lock()
	e.chooseLocked(cfg)
	e.mu.Unlock()
	e.save()
	return nil
}

func (e *Engine) rotate(ctx context.Context, id, why string) error {
	cfg := e.store.Get()
	x := cfg.Exit(id)
	if x == nil || x.Kind != config.WARP {
		return fmt.Errorf("only WARP exits can get a new IP")
	}
	old := ""
	if st := e.Exit(id); st != nil {
		old = st.Trace.IP
	}
	acc, err := warp.Register(ctx, x.License)
	if acc == nil {
		e.event("error", fmt.Sprintf("%s: IP تازه گرفته نشد: %v", x.Name, err))
		return err
	}
	if err := e.store.Update(func(c *config.Config) error {
		if ex := c.Exit(id); ex != nil {
			ex.Account = acc
		}
		return nil
	}); err != nil {
		return err
	}
	e.Reload()
	text := fmt.Sprintf("%s: حساب تازه (%s)", x.Name, why)
	if old != "" {
		text += "، IP قبلی " + old
	}
	e.event("rotate", text)
	return nil
}

// serviceExitLocked is the exit for a host of an AI service: the one the
// engine chose for it, or else the first that is up.
func (e *Engine) serviceExitLocked(cfg config.Config, host string) (exitID, svc string, ok bool) {
	for _, s := range cfg.Services {
		if !s.Enabled || !matches(host, s.Domains) {
			continue
		}
		if st := e.services[s.ID]; st != nil && e.dialers[st.Exit] != nil {
			return st.Exit, s.ID, true
		}
		for _, x := range cfg.Exits {
			if e.dialers[x.ID] != nil {
				return x.ID, s.ID, true
			}
		}
	}
	return "", "", false
}

type downDialer struct{}

func (downDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("this proxy's exit is down")
}
func (downDialer) Close() error { return nil }

func matches(host string, domains []string) bool {
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// DialVia dials through one exit (for the panel's "test" and the local API).
func (e *Engine) Dialer(id string) exit.Dialer {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dialers[id]
}

func (e *Engine) Exit(id string) *ExitState {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s := e.exits[id]; s != nil {
		c := *s
		return &c
	}
	return nil
}

func (e *Engine) event(kind, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventLocked(kind, text)
}

func (e *Engine) eventLocked(kind, text string) {
	log.Printf("%s: %s", kind, text)
	e.events = append(e.events, Event{At: time.Now(), Kind: kind, Text: text})
	if len(e.events) > 200 {
		e.events = e.events[len(e.events)-200:]
	}
}

// Snapshot is everything the panel and the local API show.
type Snapshot struct {
	Exits    []ExitState    `json:"exits"`
	Services []ServiceState `json:"services"`
	Events   []Event        `json:"events"`
	LastRun  time.Time      `json:"last_run"`
	Running  bool           `json:"running"`
	NextRun  time.Time      `json:"next_run"`
}

func (e *Engine) Snapshot() Snapshot {
	cfg := e.store.Get()
	e.mu.Lock()
	defer e.mu.Unlock()
	var s Snapshot
	for _, x := range cfg.Exits {
		if st := e.exits[x.ID]; st != nil {
			c := *st
			s.Exits = append(s.Exits, c)
		}
	}
	for _, sv := range cfg.Services {
		if st := e.services[sv.ID]; st != nil {
			s.Services = append(s.Services, *st)
		}
	}
	s.Events = slices.Clone(e.events)
	slices.Reverse(s.Events)
	if len(s.Events) > 50 {
		s.Events = s.Events[:50]
	}
	s.LastRun, s.Running = e.lastRun, e.running
	if !e.lastRun.IsZero() {
		s.NextRun = e.lastRun.Add(time.Duration(cfg.CheckMinutes) * time.Minute)
	}
	return s
}

type saved struct {
	Exits    map[string]*ExitState    `json:"exits"`
	Services map[string]*ServiceState `json:"services"`
	Events   []Event                  `json:"events"`
	LastRun  time.Time                `json:"last_run"`
}

func (e *Engine) save() {
	e.mu.Lock()
	b, _ := json.Marshal(saved{e.exits, e.services, e.events, e.lastRun})
	e.mu.Unlock()
	p := filepath.Join(e.dir, "state.json")
	if os.WriteFile(p+".tmp", b, 0o600) == nil {
		os.Rename(p+".tmp", p)
	}
}

func (e *Engine) load() {
	b, err := os.ReadFile(filepath.Join(e.dir, "state.json"))
	if err != nil {
		return
	}
	var s saved
	if json.Unmarshal(b, &s) != nil {
		return
	}
	for id, st := range s.Exits {
		st.Checking = false
		if st.Results == nil {
			st.Results = map[string]check.Result{}
		}
		e.exits[id] = st
	}
	for id, st := range s.Services {
		e.services[id] = st
	}
	e.events, e.lastRun = s.Events, s.LastRun
}

// DialDirect is the server's own route, used when Route returns nil.
func DialDirect(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = 10 * time.Second
	return d.DialContext(ctx, "tcp", addr)
}

// friendly turns the usual dial errors into words.
func friendly(kind string, err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case kind == config.Direct6 && (strings.Contains(low, "no suitable address") || strings.Contains(low, "requested name is valid") || strings.Contains(low, "network is unreachable") || strings.Contains(low, "no route")):
		return "این سرور IPv6 ندارد"
	case strings.Contains(low, "timed out") || strings.Contains(low, "timeout"):
		return "جواب نداد (timeout)"
	case strings.Contains(low, "connection refused"):
		return "اتصال رد شد"
	}
	return s
}
