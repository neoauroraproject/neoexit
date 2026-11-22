package web

// Proxy ports: the main one ("main") and the extra ones, each with its
// own login; each follows the routing table or sends everything to one
// exit.

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"neoexit/internal/config"
)

type extraView struct {
	config.ExtraProxy
	Running  bool           `json:"running"`
	Conns    int64          `json:"conns"`
	Outbound map[string]any `json:"outbound"` // for Xray / 3x-ui
}

func (a *App) extraViews(cfg config.Config) []extraView {
	out := []extraView{}
	for _, x := range cfg.Extra {
		v := extraView{ExtraProxy: x, Outbound: ExtraOutbound(x)}
		if a.Extras != nil {
			v.Running, v.Conns = a.Extras.Running(x.ID), a.Extras.Active(x.ID)
		}
		out = append(out, v)
	}
	return out
}

// ExtraOutbound is the Xray outbound for one extra proxy, tagged
// "neoexit-<id>" so several can sit side by side.
func ExtraOutbound(x config.ExtraProxy) map[string]any {
	server := map[string]any{"address": "127.0.0.1", "port": x.Port}
	if x.User != "" {
		server["users"] = []map[string]string{{"user": x.User, "pass": x.Pass}}
	}
	return map[string]any{"tag": "neoexit-" + x.ID, "protocol": "socks", "settings": map[string]any{"servers": []any{server}}}
}

type extraInput struct {
	Name   *string `json:"name"`
	Listen *string `json:"listen"`
	Port   *int    `json:"port"`
	User   *string `json:"user"`
	Pass   *string `json:"pass"`
	Exit   *string `json:"exit"`
}

func (in extraInput) apply(x *config.ExtraProxy) {
	if in.Name != nil {
		x.Name = strings.TrimSpace(*in.Name)
	}
	if in.Listen != nil {
		x.Listen = *in.Listen
	}
	if in.Port != nil {
		x.Port = *in.Port
	}
	if in.User != nil {
		x.User = strings.TrimSpace(*in.User)
	}
	if in.Pass != nil {
		x.Pass = *in.Pass
	}
	if in.Exit != nil {
		x.Exit = *in.Exit
	}
}

// trimList splits pasted lines and commas (as 3x-ui takes them) and drops
// blanks.
func trimList(in []string) []string {
	var out []string
	for _, s := range in {
		for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == '،' }) {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}

// changeExtras saves a change to the extra proxies and opens their ports
// again; if a port can't open, the change is undone.
func (a *App) changeExtras(w http.ResponseWriter, f func(c *config.Config) error) bool {
	old := a.Store.Get().Extra
	if err := a.Store.Update(f); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	if a.RestartExtras == nil {
		return true
	}
	if err := a.RestartExtras(); err != nil {
		a.Store.Update(func(c *config.Config) error { c.Extra = old; return nil })
		a.RestartExtras()
		writeErr(w, http.StatusBadRequest, "پورت باز نشد: "+err.Error())
		return false
	}
	return true
}

func (a *App) addExtra(w http.ResponseWriter, r *http.Request) {
	var in extraInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	var id string
	ok := a.changeExtras(w, func(c *config.Config) error {
		id = c.NewExtraID()
		x := config.ExtraProxy{ID: id, Name: "پروکسی " + strings.TrimPrefix(id, "p"), Listen: "127.0.0.1", Port: config.FreePort(20000, 40000)}
		in.apply(&x)
		if x.Name == "" {
			return errors.New("اسم خالی است")
		}
		c.Extra = append(c.Extra, x)
		return nil
	})
	if ok {
		writeJSON(w, map[string]string{"id": id})
	}
}

// editExtra changes an extra proxy, or the main proxy's exit (id "main";
// its address and login are changed with PUT api/proxy).
func (a *App) editExtra(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in extraInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if id == "main" {
		err := a.Store.Update(func(c *config.Config) error {
			if in.Exit != nil {
				c.Proxy.Exit = *in.Exit
			}
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	ok := a.changeExtras(w, func(c *config.Config) error {
		x := c.ExtraProxy(id)
		if x == nil {
			return errors.New("این پروکسی دیگر وجود ندارد")
		}
		in.apply(x)
		if x.Name == "" {
			return errors.New("اسم خالی است")
		}
		return nil
	})
	if ok {
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (a *App) deleteExtra(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ok := a.changeExtras(w, func(c *config.Config) error {
		c.Extra = slices.DeleteFunc(c.Extra, func(x config.ExtraProxy) bool { return x.ID == id })
		// Drop the name from rules; a rule that named only this proxy would
		// otherwise apply to all of them, so it is switched off.
		for i := range c.Routing {
			x := &c.Routing[i]
			had := len(x.Inbounds)
			x.Inbounds = slices.DeleteFunc(x.Inbounds, func(s string) bool { return s == id })
			if had > 0 && len(x.Inbounds) == 0 {
				x.Disabled = true
			}
		}
		return nil
	})
	if ok {
		writeJSON(w, map[string]bool{"ok": true})
	}
}
