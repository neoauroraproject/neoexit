// Package update finds new NeoExit releases on GitHub and installs one in
// place: it downloads the binary for this CPU, checks it against the
// release's SHA256SUMS, makes sure it runs, swaps it in and asks the
// service to restart (systemd starts the new one).
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const Repo = "neoauroraproject/neoexit"

// Overridden in tests.
var (
	apiURL      = "https://api.github.com/repos/" + Repo + "/releases/latest"
	downloadURL = "https://github.com/" + Repo + "/releases/download/"
)

const every = 6 * time.Hour

// Status is what the panel shows.
type Status struct {
	Current string    `json:"current"`
	Latest  string    `json:"latest,omitempty"`
	Newer   bool      `json:"newer"`
	Notes   string    `json:"notes,omitempty"`
	URL     string    `json:"url,omitempty"`
	Checked time.Time `json:"checked,omitempty"`
	Err     string    `json:"err,omitempty"`   // the last check failed
	State   string    `json:"state,omitempty"` // "", downloading, restarting, manual, failed
	Problem string    `json:"problem,omitempty"`
	// Can: this install can update itself (Linux, the binary is writable).
	Can bool `json:"can"`
}

type Updater struct {
	Current string
	// Restart ends the service so systemd starts the new binary; nil
	// means send ourselves SIGTERM.
	Restart func()

	mu      sync.Mutex
	st      Status
	once    sync.Once
	tonce   sync.Once
	exe     string
	client  *http.Client
	running bool
}

func (u *Updater) http() *http.Client {
	if u.client == nil {
		u.client = &http.Client{Timeout: 5 * time.Minute}
	}
	return u.client
}

// Start checks a minute after start-up and then every six hours.
func (u *Updater) Start() {
	u.once.Do(func() {
		go func() {
			time.Sleep(time.Minute)
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				u.Check(ctx)
				cancel()
				time.Sleep(every)
			}
		}()
	})
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.st
	st.Current = u.Current
	st.Newer = st.Latest != "" && Newer(st.Latest, u.Current)
	_, st.Can = u.target()
	return st
}

// Check asks GitHub for the latest release.
func (u *Updater) Check(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "NeoExit/"+u.Current)
	var r struct {
		Tag   string `json:"tag_name"`
		Body  string `json:"body"`
		URL   string `json:"html_url"`
		Draft bool   `json:"draft"`
		Pre   bool   `json:"prerelease"`
	}
	err := func() error {
		res, err := u.http().Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("GitHub: %s", res.Status)
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&r); err != nil {
			return err
		}
		if _, ok := parse(r.Tag); !ok {
			return fmt.Errorf("GitHub: unexpected release %q", r.Tag)
		}
		return nil
	}()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.st.Checked = time.Now()
	if err != nil {
		u.st.Err = err.Error()
		return err
	}
	u.st.Err = ""
	u.st.Latest, u.st.URL = r.Tag, r.URL
	u.st.Notes = clip(r.Body, 2000)
	return nil
}

// target is the running binary, if this install can replace it (found
// once: Status is asked on every panel poll).
func (u *Updater) target() (string, bool) {
	u.tonce.Do(func() {
		if runtime.GOOS != "linux" {
			return
		}
		exe, err := os.Executable()
		if err != nil {
			return
		}
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
		f, err := os.CreateTemp(filepath.Dir(exe), ".neoexit-w*")
		if err != nil {
			return
		}
		f.Close()
		os.Remove(f.Name())
		u.exe = exe
	})
	return u.exe, u.exe != ""
}

// Apply installs the latest release in the background; Status follows it.
func (u *Updater) Apply() error {
	u.mu.Lock()
	tag := u.st.Latest
	switch {
	case u.running:
		u.mu.Unlock()
		return errors.New("به‌روزرسانی در جریان است")
	case tag == "" || !Newer(tag, u.Current):
		u.mu.Unlock()
		return errors.New("نسخه‌ی تازه‌تری نیست")
	}
	exe, ok := u.target()
	if !ok {
		u.mu.Unlock()
		return errors.New("این نصب خودش به‌روز نمی‌شود؛ دستور نصب را دوباره اجرا کنید")
	}
	u.running = true
	u.st.State, u.st.Problem = "downloading", ""
	u.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := u.install(ctx, tag, exe)
		u.mu.Lock()
		u.running = false
		if err != nil {
			u.st.State, u.st.Problem = "failed", err.Error()
			u.mu.Unlock()
			return
		}
		// Under systemd (it sets INVOCATION_ID) a restart starts the new
		// binary; run by hand, the person restarts it.
		if os.Getenv("INVOCATION_ID") == "" {
			u.st.State = "manual"
			u.mu.Unlock()
			return
		}
		u.st.State = "restarting"
		u.mu.Unlock()
		time.Sleep(1500 * time.Millisecond) // let the panel see it
		if u.Restart != nil {
			u.Restart()
		} else if p, err := os.FindProcess(os.Getpid()); err == nil {
			p.Signal(syscall.SIGTERM)
		}
	}()
	return nil
}

func (u *Updater) install(ctx context.Context, tag, exe string) error {
	name := "neoexit-linux-" + runtime.GOARCH
	base := downloadURL + tag + "/"
	sums, err := u.get(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return fmt.Errorf("SHA256SUMS: %w", err)
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if len(want) != 64 {
		return fmt.Errorf("%s is not in SHA256SUMS", name)
	}
	bin, err := u.get(ctx, base+name, 200<<20)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if s := sha256.Sum256(bin); hex.EncodeToString(s[:]) != want {
		return errors.New("checksum mismatch; nothing was changed")
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	// The new binary must run and say it is the version we asked for.
	out, err := exec.CommandContext(ctx, tmp, "version").Output()
	if got := strings.TrimSpace(string(out)); err != nil || strings.TrimPrefix(got, "v") != strings.TrimPrefix(tag, "v") {
		os.Remove(tmp)
		return fmt.Errorf("the new binary did not run (%q, %v)", got, err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (u *Updater) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "NeoExit/"+u.Current)
	res, err := u.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New(res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err == nil && int64(len(b)) > max {
		err = errors.New("too large")
	}
	return b, err
}

// parse reads "v1.2.3" or "1.2.3-dev".
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Newer reports whether version a is later than b. A "-dev" build counts
// as its own number, so v1.2.1 is not newer than 1.2.1-dev.
func Newer(a, b string) bool {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
