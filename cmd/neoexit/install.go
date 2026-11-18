package main

// "neoexit install" sets this binary up as the service, with no questions:
// what install.sh does after its download, and what NeoTunnel runs to
// install NeoExit on any of its servers from its panel.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/web"
)

const (
	binPath  = "/usr/local/bin/neoexit"
	unitPath = "/etc/systemd/system/neoexit.service"
	unit     = `[Unit]
Description=NeoExit: free exits for AI services
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/neoexit run
Restart=always
RestartSec=3
LimitNOFILE=1048576
RuntimeDirectory=neoexit
RuntimeDirectoryMode=0755

[Install]
WantedBy=multi-user.target
`
)

// Installed is what "install --json" prints.
type Installed struct {
	Version string `json:"version"`
	User    string `json:"user"`
	// Password is set only on a first install; it is shown once.
	Password  string `json:"password,omitempty"`
	PanelPort int    `json:"panel_port"`
	PanelPath string `json:"panel_path"`
	Proxy     string `json:"proxy"`
	Running   bool   `json:"running"`
}

func install(dir, psiphonBin string, asJSON bool) error {
	if os.Geteuid() != 0 {
		return errors.New("run as root")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if exe != binPath {
		if err := copyFile(exe, binPath, 0o755); err != nil {
			return fmt.Errorf("install the program: %w", err)
		}
	}
	// The Psiphon client, for a server that can't download it itself.
	if psiphonBin != "" {
		if err := copyFile(psiphonBin, filepath.Join(dir, "psiphon", "psiphon-client"), 0o755); err != nil {
			return fmt.Errorf("install the Psiphon client: %w", err)
		}
	}
	cfgPath := filepath.Join(dir, "config.json")
	pass := ""
	store, err := config.Open(cfgPath)
	if os.IsNotExist(err) {
		store, pass, err = config.Create(cfgPath)
	}
	if err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "neoexit"}, {"restart", "neoexit"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v %s", args[0], err, out)
		}
	}
	c := store.Get()
	res := Installed{Version: version, User: c.Panel.User, Password: pass, PanelPort: c.Panel.Port, PanelPath: c.Panel.Path,
		Proxy: net.JoinHostPort(c.Proxy.Listen, strconv.Itoa(c.Proxy.Port)), Running: waitSocket(15 * time.Second)}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(res)
	}
	fmt.Printf("✓ NeoExit %s installed and running\n", version)
	printLogin(c, pass, false)
	if pass != "" {
		fmt.Println("The password is shown only now. Forgot it? Run: neoexit password")
	}
	return nil
}

func uninstall(dir string, keep bool) error {
	if os.Geteuid() != 0 {
		return errors.New("run as root")
	}
	exec.Command("systemctl", "disable", "--now", "neoexit").Run()
	os.Remove(unitPath)
	exec.Command("systemctl", "daemon-reload").Run()
	os.Remove(binPath)
	os.RemoveAll(filepath.Dir(web.SocketPath))
	if !keep {
		os.RemoveAll(dir)
	}
	fmt.Println("✓ NeoExit removed")
	return nil
}

// waitSocket waits for the new service's local API to answer.
func waitSocket(d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, err := (&net.Dialer{}).DialContext(ctx, "unix", web.SocketPath)
		cancel()
		if err == nil {
			c.Close()
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// copyFile writes src to dst through a temporary file and a rename, so a
// running dst is replaced, not overwritten in place.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".neoexit-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
