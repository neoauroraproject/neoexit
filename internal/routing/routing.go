// Package routing carries the HMray Smart Routing lists (hmray.dat for
// domains, hmray-geoip.dat for IPs) and puts them where 3x-ui's Xray reads
// its geo files, so rules like "ext:hmray.dat:ai" work.
package routing

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	//go:embed data/hmray.dat
	siteDat []byte
	//go:embed data/hmray-geoip.dat
	ipDat []byte
)

// File is one of the two lists.
type File struct {
	Name string `json:"name"`
	Size int    `json:"size"`
	Sum  string `json:"sum"` // first 12 hex digits of its SHA-256
	data []byte
}

// Files are the lists, domains first.
var Files = []File{file("hmray.dat", siteDat), file("hmray-geoip.dat", ipDat)}

func file(name string, b []byte) File {
	s := sha256.Sum256(b)
	return File{Name: name, Size: len(b), Sum: hex.EncodeToString(s[:6]), data: b}
}

// Data returns a list's contents by name.
func Data(name string) ([]byte, bool) {
	for _, f := range Files {
		if f.Name == name {
			return f.data, true
		}
	}
	return nil, false
}

// XUIDir is 3x-ui's folder for Xray and its geo files ("" when 3x-ui is
// not on this server).
var XUIDir = func() string {
	for _, d := range []string{os.Getenv("XUI_BIN_FOLDER"), "/usr/local/x-ui/bin"} {
		if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// Status is whether the lists in dir are there and the same as these.
type Status struct {
	Dir       string `json:"dir"`
	Installed bool   `json:"installed"` // both files are there
	Current   bool   `json:"current"`   // and both are this version
}

// Check is asked on every panel poll, so a file is read again only when
// its size or time changed.
func Check(dir string) Status {
	st := Status{Dir: dir}
	if dir == "" {
		return st
	}
	st.Installed, st.Current = true, true
	for _, f := range Files {
		p := filepath.Join(dir, f.Name)
		fi, err := os.Stat(p)
		if err != nil {
			st.Installed, st.Current = false, false
			break
		}
		if !same(p, fi, f.data) {
			st.Current = false
		}
	}
	return st
}

type seen struct {
	size int64
	mod  time.Time
	same bool
}

var (
	seenMu sync.Mutex
	seenAt = map[string]seen{}
)

func same(p string, fi os.FileInfo, want []byte) bool {
	seenMu.Lock()
	defer seenMu.Unlock()
	if s, ok := seenAt[p]; ok && s.size == fi.Size() && s.mod.Equal(fi.ModTime()) {
		return s.same
	}
	eq := false
	if fi.Size() == int64(len(want)) {
		b, err := os.ReadFile(p)
		eq = err == nil && bytes.Equal(b, want)
	}
	seenAt[p] = seen{fi.Size(), fi.ModTime(), eq}
	return eq
}

// Install writes the lists into dir, replacing older ones. It reports
// whether anything changed (Xray reads them only when it starts).
func Install(dir string) (bool, error) {
	changed := false
	for _, f := range Files {
		p := filepath.Join(dir, f.Name)
		if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, f.data) {
			continue
		}
		tmp := p + ".neoexit-tmp"
		if err := os.WriteFile(tmp, f.data, 0o644); err != nil {
			return changed, err
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			return changed, err
		}
		changed = true
	}
	return changed, nil
}
