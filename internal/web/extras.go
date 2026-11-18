package web

// Updates of NeoExit itself, and the Smart Routing lists for 3x-ui.

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/routing"
	"neoexit/internal/update"
)

// --- updates -------------------------------------------------------------

func (a *App) updateStatus() update.Status {
	if a.Updater == nil {
		return update.Status{Current: a.Version}
	}
	return a.Updater.Status()
}

// POST api/update/check: ask GitHub now.
func (a *App) updateCheck(w http.ResponseWriter, r *http.Request) {
	if a.Updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "به‌روزرسانی در دسترس نیست")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.Updater.Check(ctx); err != nil {
		writeErr(w, http.StatusBadGateway, "GitHub جواب نداد: "+err.Error())
		return
	}
	writeJSON(w, a.Updater.Status())
}

// POST api/update/apply: download, check and install the latest release,
// then restart.
func (a *App) updateApply(w http.ResponseWriter, r *http.Request) {
	if a.Updater == nil {
		writeErr(w, http.StatusServiceUnavailable, "به‌روزرسانی در دسترس نیست")
		return
	}
	if err := a.Updater.Apply(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, a.Updater.Status())
}

// --- Smart Routing lists -------------------------------------------------

type routingView struct {
	Files  []routing.File `json:"files"`
	Status routing.Status `json:"status"` // in 3x-ui's folder
	Auto   bool           `json:"auto"`   // kept current after updates
}

func (a *App) routingState(cfg config.Config) routingView {
	return routingView{Files: routing.Files, Status: routing.Check(routing.XUIDir()), Auto: cfg.RoutingFiles}
}

// keepRouting refreshes the lists in 3x-ui's folder at start-up when they
// were installed from the panel (a new NeoExit may carry newer ones).
func (a *App) keepRouting() {
	dir := routing.XUIDir()
	if !a.Store.Get().RoutingFiles || dir == "" {
		return
	}
	if changed, err := routing.Install(dir); err != nil {
		log.Printf("routing lists: %v", err)
	} else if changed {
		log.Printf("routing lists updated in %s; restart Xray in 3x-ui to load them", dir)
	}
}

// POST api/routing/install: put the lists in 3x-ui's folder.
func (a *App) routingInstall(w http.ResponseWriter, r *http.Request) {
	dir := routing.XUIDir()
	if dir == "" {
		writeErr(w, http.StatusBadRequest, "3x-ui روی این سرور پیدا نشد؛ فایل‌ها را دانلود کنید و دستی بگذارید")
		return
	}
	changed, err := routing.Install(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "نوشتن در "+dir+" نشد: "+err.Error())
		return
	}
	a.Store.Update(func(c *config.Config) error { c.RoutingFiles = true; return nil })
	writeJSON(w, map[string]any{"changed": changed, "routing": a.routingState(a.Store.Get())})
}

// GET api/routing/files/{name}: download a list.
func (a *App) routingFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	b, ok := routing.Data(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Write(b)
}
