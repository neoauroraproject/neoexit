// Package web is the panel (one page, behind a secret path and a
// password) and the local API on a unix socket that NeoTunnel reads.
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/engine"
	"neoexit/internal/opera"
	"neoexit/internal/proxy"
	"neoexit/internal/psiphon"
)

//go:embed static
var static embed.FS

type App struct {
	Store   *config.Store
	Engine  *engine.Engine
	Proxy   *proxy.Server
	Version string
	// RestartProxy re-opens the proxy after its address changed.
	RestartProxy func() error

	failMu sync.Mutex
	fails  map[string][]time.Time
	spots  spots
}

// Handler is the panel at /<path>/.
func (a *App) Handler() http.Handler {
	cfg := a.Store.Get()
	base := "/" + cfg.Panel.Path
	mux := http.NewServeMux()
	files, _ := fs.Sub(static, "static")
	fileServer := http.StripPrefix(base+"/", http.FileServer(http.FS(files)))
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/", http.StatusFound)
	})
	mux.Handle(base+"/", headers(fileServer))
	mux.HandleFunc("POST "+base+"/api/login", a.login)
	mux.HandleFunc("POST "+base+"/api/logout", a.logout)
	a.spots.start()
	mux.HandleFunc("GET "+base+"/api/spot", a.spot)
	api := http.NewServeMux()
	api.HandleFunc("GET /api/state", a.state)
	api.HandleFunc("POST /api/check", a.checkNow)
	api.HandleFunc("POST /api/exits", a.addExit)
	api.HandleFunc("PUT /api/exits/{id}", a.editExit)
	api.HandleFunc("DELETE /api/exits/{id}", a.deleteExit)
	api.HandleFunc("POST /api/exits/{id}/move", a.moveExit)
	api.HandleFunc("POST /api/exits/{id}/rotate", a.rotateExit)
	api.HandleFunc("POST /api/services", a.addService)
	api.HandleFunc("PUT /api/services/{id}", a.editService)
	api.HandleFunc("DELETE /api/services/{id}", a.deleteService)
	api.HandleFunc("PUT /api/settings", a.settings)
	api.HandleFunc("PUT /api/proxy", a.editProxy)
	api.HandleFunc("PUT /api/password", a.password)
	api.HandleFunc("POST /api/spot/open", a.spotOpen)
	mux.Handle(base+"/api/", http.StripPrefix(base, a.authed(api)))
	// Anything else: look like nothing is here.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return mux
}

func headers(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'")
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// --- sessions ---------------------------------------------------------

const cookieName = "neoexit"

func (a *App) sign(exp int64) string {
	m := hmac.New(sha256.New, []byte(a.Store.Get().Panel.Key))
	fmt.Fprintf(m, "%d", exp)
	return fmt.Sprintf("%d.%s", exp, hex.EncodeToString(m.Sum(nil)))
}

func (a *App) valid(v string) bool {
	exp, _, ok := strings.Cut(v, ".")
	n, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil || time.Now().Unix() > n {
		return false
	}
	return hmac.Equal([]byte(v), []byte(a.sign(n)))
}

func (a *App) authed(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !a.valid(c.Value) {
			writeErr(w, http.StatusUnauthorized, "وارد نشده‌اید")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-NeoExit") != "1" {
			writeErr(w, http.StatusForbidden, "bad request origin")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

// tooMany: 8 wrong passwords in 15 minutes locks an address out.
func (a *App) tooMany(ip string, fail bool) bool {
	a.failMu.Lock()
	defer a.failMu.Unlock()
	if a.fails == nil {
		a.fails = map[string][]time.Time{}
	}
	cut := time.Now().Add(-15 * time.Minute)
	l := slices.DeleteFunc(a.fails[ip], func(t time.Time) bool { return t.Before(cut) })
	if fail {
		l = append(l, time.Now())
	}
	a.fails[ip] = l
	return len(l) >= 8
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if a.tooMany(ip, false) {
		writeErr(w, http.StatusTooManyRequests, "تلاش‌های ناموفق زیاد بود؛ ۱۵ دقیقه‌ی دیگر دوباره امتحان کنید")
		return
	}
	var in struct{ User, Pass string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if !a.Store.Get().Panel.CheckPassword(in.User, in.Pass) {
		a.tooMany(ip, true)
		time.Sleep(700 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, "نام کاربری یا رمز درست نیست")
		return
	}
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: a.sign(exp), Path: "/" + a.Store.Get().Panel.Path,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, Expires: time.Unix(exp, 0)})
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/" + a.Store.Get().Panel.Path, MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

// --- state ------------------------------------------------------------

type exitView struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Address string `json:"address,omitempty"`
	User    string `json:"user,omitempty"`
	HasPass bool   `json:"has_pass,omitempty"`
	License bool   `json:"license,omitempty"`
	Plus    bool   `json:"plus,omitempty"`
	Region  string `json:"region,omitempty"`
	HasKey  bool   `json:"has_key,omitempty"`
}

// Choices are the regions and countries the panel offers per kind.
type Choices struct {
	Opera   []string `json:"opera"`
	Psiphon []string `json:"psiphon"`
}

type stateView struct {
	Version  string           `json:"version"`
	Exits    []exitView       `json:"exits"`
	Services []config.Service `json:"services"`
	Proxy    config.Proxy     `json:"proxy"`
	Settings settingsView     `json:"settings"`
	User     string           `json:"user"`
	Live     engine.Snapshot  `json:"live"`
	Conns    int64            `json:"conns"`
	Host     HostInfo         `json:"host"`
	Snippet  map[string]any   `json:"snippet"`
	Choices  Choices          `json:"choices"`
}

type settingsView struct {
	CheckMinutes int    `json:"check_minutes"`
	SameCountry  bool   `json:"same_country"`
	AutoRotate   bool   `json:"auto_rotate"`
	RotateHourly int    `json:"rotate_hourly"`
	Other        string `json:"other"`
}

func (a *App) state(w http.ResponseWriter, r *http.Request) {
	cfg := a.Store.Get()
	v := stateView{
		Version:  a.Version,
		Services: cfg.Services,
		Proxy:    cfg.Proxy,
		User:     cfg.Panel.User,
		Live:     a.Engine.Snapshot(),
		Conns:    a.Proxy.Active.Load(),
		Host:     Host(),
		Snippet:  Snippet(cfg),
		Settings: settingsView{cfg.CheckMinutes, cfg.SameCountry, cfg.AutoRotate, cfg.RotateHourly, cfg.Other},
		Choices:  Choices{Opera: opera.Regions, Psiphon: psiphon.Countries},
	}
	for _, x := range cfg.Exits {
		ev := exitView{ID: x.ID, Kind: x.Kind, Name: x.Name, Enabled: x.Enabled, Address: x.Address, User: x.User,
			HasPass: x.Pass != "", License: x.License != "", Region: x.Region, HasKey: x.Key != ""}
		if x.Account != nil {
			ev.Plus = x.Account.Plus
		}
		v.Exits = append(v.Exits, ev)
	}
	writeJSON(w, v)
}

func (a *App) checkNow(w http.ResponseWriter, r *http.Request) {
	a.Engine.CheckNow()
	writeJSON(w, map[string]bool{"ok": true})
}

// --- exits ------------------------------------------------------------

type exitInput struct {
	Kind    string  `json:"kind"`
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
	Address *string `json:"address"`
	User    *string `json:"user"`
	Pass    *string `json:"pass"`
	License *string `json:"license"`
	Region  *string `json:"region"`
	Key     *string `json:"key"`
}

func (in exitInput) apply(x *config.Exit) {
	oldAddr := x.Address
	defer func() {
		if x.Kind == config.SSH && x.Address != oldAddr {
			x.HostKey = "" // a different server: learn its key again
		}
	}()
	if in.Name != nil {
		x.Name = strings.TrimSpace(*in.Name)
	}
	if in.Enabled != nil {
		x.Enabled = *in.Enabled
	}
	if in.Address != nil {
		x.Address = strings.TrimSpace(*in.Address)
	}
	if in.User != nil {
		x.User = *in.User
	}
	if in.Pass != nil {
		x.Pass = *in.Pass
	}
	if in.Region != nil {
		x.Region = strings.ToUpper(strings.TrimSpace(*in.Region))
	}
	if in.Key != nil && strings.TrimSpace(*in.Key) != "" {
		x.Key = strings.TrimSpace(*in.Key) + "\n"
	}
	if in.Address != nil && x.Kind == config.SSH && !strings.Contains(x.Address, ":") && x.Address != "" {
		x.Address += ":22"
	}
	if x.Kind == config.Opera && x.Region == "" {
		x.Region = "EU"
	}
	if in.License != nil && *in.License != x.License {
		x.License = strings.TrimSpace(*in.License)
		if x.Kind == config.WARP {
			x.Account = nil // register again with the key
		}
	}
}

func (a *App) addExit(w http.ResponseWriter, r *http.Request) {
	var in exitInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || !slices.Contains(config.Kinds, in.Kind) {
		writeErr(w, http.StatusBadRequest, "نوع خروجی را انتخاب کنید")
		return
	}
	// Parse "socks5://user:pass@host:port" pasted into the address field.
	if in.Address != nil && strings.Contains(*in.Address, "://") {
		if u, err := parseProxyURL(*in.Address); err == nil {
			in.Address, in.User, in.Pass = &u[0], &u[1], &u[2]
		}
	}
	var id string
	err := a.Store.Update(func(c *config.Config) error {
		id = c.NewExitID(in.Kind)
		x := config.Exit{ID: id, Kind: in.Kind, Name: defaultName(in.Kind), Enabled: true}
		in.apply(&x)
		if x.Name == "" {
			x.Name = defaultName(in.Kind)
		}
		c.Exits = append(c.Exits, x)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	a.Engine.CheckNow()
	writeJSON(w, map[string]string{"id": id})
}

func defaultName(kind string) string {
	switch kind {
	case config.WARP:
		return "WARP"
	case config.Direct4:
		return "IPv4 سرور"
	case config.Direct6:
		return "IPv6 سرور"
	case config.SOCKS5:
		return "SOCKS5"
	case config.Opera:
		return "Opera"
	case config.Psiphon:
		return "سایفون"
	case config.SSH:
		return "SSH"
	}
	return "HTTP"
}

func (a *App) editExit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in exitInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	err := a.Store.Update(func(c *config.Config) error {
		x := c.Exit(id)
		if x == nil {
			return errors.New("این خروجی دیگر وجود ندارد")
		}
		in.apply(x)
		if x.Name == "" {
			return errors.New("اسم خالی است")
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	a.Engine.CheckNow()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) deleteExit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.Store.Update(func(c *config.Config) error {
		c.Exits = slices.DeleteFunc(c.Exits, func(x config.Exit) bool { return x.ID == id })
		for i := range c.Services {
			if c.Services[i].Pin == id {
				c.Services[i].Pin = ""
			}
		}
		if c.Other == id {
			c.Other = ""
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) moveExit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct{ Dir int }
	json.NewDecoder(r.Body).Decode(&in)
	err := a.Store.Update(func(c *config.Config) error {
		i := slices.IndexFunc(c.Exits, func(x config.Exit) bool { return x.ID == id })
		j := i + in.Dir
		if i < 0 || j < 0 || j >= len(c.Exits) {
			return nil
		}
		c.Exits[i], c.Exits[j] = c.Exits[j], c.Exits[i]
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) rotateExit(w http.ResponseWriter, r *http.Request) {
	if err := a.Engine.Rotate(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// --- services and settings -------------------------------------------

func (a *App) editService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in serviceInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	err := a.Store.Update(func(c *config.Config) error {
		sv := c.Service(id)
		if sv == nil {
			return errors.New("این سرویس دیگر وجود ندارد")
		}
		return in.apply(sv)
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	if (in.Enabled != nil && *in.Enabled) || in.Domains != nil {
		a.Engine.CheckNow()
	}
	writeJSON(w, map[string]bool{"ok": true})
}

type serviceInput struct {
	Name    *string   `json:"name"`
	Enabled *bool     `json:"enabled"`
	Pin     *string   `json:"pin"`
	Domains *[]string `json:"domains"`
}

func (in serviceInput) apply(sv *config.Service) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 40 {
			return errors.New("اسم سرویس خالی یا خیلی بلند است")
		}
		sv.Name = name
	}
	if in.Enabled != nil {
		sv.Enabled = *in.Enabled
	}
	if in.Pin != nil {
		sv.Pin = *in.Pin
	}
	if in.Domains != nil {
		d, err := config.NormalizeDomains(*in.Domains)
		if err != nil {
			return err
		}
		sv.Domains = d
	}
	return nil
}

func (a *App) addService(w http.ResponseWriter, r *http.Request) {
	var in serviceInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Name == nil || in.Domains == nil {
		writeErr(w, http.StatusBadRequest, "اسم و دامنه‌ها لازم است")
		return
	}
	var id string
	err := a.Store.Update(func(c *config.Config) error {
		sv := config.Service{Enabled: true, Custom: true}
		if err := in.apply(&sv); err != nil {
			return err
		}
		sv.ID = c.NewServiceID(sv.Name)
		id = sv.ID
		c.Services = append(c.Services, sv)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	a.Engine.CheckNow()
	writeJSON(w, map[string]string{"id": id})
}

func (a *App) deleteService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.Store.Update(func(c *config.Config) error {
		sv := c.Service(id)
		if sv == nil {
			return nil
		}
		if !sv.Custom {
			return errors.New("سرویس‌های پیش‌فرض حذف نمی‌شوند؛ خاموشش کنید")
		}
		c.Services = slices.DeleteFunc(c.Services, func(s config.Service) bool { return s.ID == id })
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	var in settingsView
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.CheckMinutes < 2 || in.CheckMinutes > 720 {
		writeErr(w, http.StatusBadRequest, "فاصله‌ی تست باید بین ۲ تا ۷۲۰ دقیقه باشد")
		return
	}
	if in.RotateHourly < 0 || in.RotateHourly > 30 {
		writeErr(w, http.StatusBadRequest, "سقف IP تازه باید بین ۰ تا ۳۰ در ساعت باشد")
		return
	}
	err := a.Store.Update(func(c *config.Config) error {
		c.CheckMinutes, c.SameCountry, c.AutoRotate, c.Other = in.CheckMinutes, in.SameCountry, in.AutoRotate, in.Other
		if in.RotateHourly > 0 {
			c.RotateHourly = in.RotateHourly
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Engine.Reload()
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) editProxy(w http.ResponseWriter, r *http.Request) {
	var in config.Proxy
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Listen != "127.0.0.1" && in.Listen != "0.0.0.0" {
		writeErr(w, http.StatusBadRequest, "bad listen address")
		return
	}
	old := a.Store.Get().Proxy
	err := a.Store.Update(func(c *config.Config) error {
		c.Proxy = in
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.RestartProxy(); err != nil {
		a.Store.Update(func(c *config.Config) error { c.Proxy = old; return nil })
		a.RestartProxy()
		writeErr(w, http.StatusBadRequest, "پورت باز نشد: "+err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *App) password(w http.ResponseWriter, r *http.Request) {
	var in struct{ Old, User, New string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	p := a.Store.Get().Panel
	if !p.CheckPassword(p.User, in.Old) {
		writeErr(w, http.StatusForbidden, "رمز فعلی درست نیست")
		return
	}
	if len(in.New) < 8 || strings.TrimSpace(in.User) == "" {
		writeErr(w, http.StatusBadRequest, "رمز تازه حداقل ۸ حرف باشد")
		return
	}
	err := a.Store.Update(func(c *config.Config) error {
		c.Panel.User = strings.TrimSpace(in.User)
		c.Panel.SetPassword(in.New)
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func parseProxyURL(s string) ([3]string, error) {
	var out [3]string
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return out, errors.New("no scheme")
	}
	_ = scheme
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		cred := rest[:at]
		rest = rest[at+1:]
		u, p, _ := strings.Cut(cred, ":")
		out[1], out[2] = u, p
	}
	out[0] = strings.TrimSuffix(rest, "/")
	return out, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var _ = os.Getenv
