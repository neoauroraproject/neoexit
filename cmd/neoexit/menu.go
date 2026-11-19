package main

// "neoexit" typed alone in a terminal opens a small menu: update, new
// password, install/repair, uninstall and the rest, each also a command
// of its own. Without a terminal (scripts, install.sh) it prints usage.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/update"
)

const (
	cOK  = "\033[38;5;78m"
	cAcc = "\033[38;5;36m"
	cErr = "\033[38;5;203m"
	cDim = "\033[2m"
	c0   = "\033[0m"
)

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func serviceActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "neoexit").Run() == nil
}

func serviceInstalled() bool {
	_, err := os.Stat(unitPath)
	return err == nil
}

func needRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("run as root (sudo neoexit ...)")
	}
	return nil
}

// resetPassword sets a new random panel password. The running service
// holds the settings in memory and would write the old password back, so
// it is stopped around the change.
func resetPassword(cfgPath string, plain bool) error {
	if err := needRoot(); err != nil && serviceInstalled() {
		return err
	}
	was := serviceActive()
	if was {
		if out, err := exec.Command("systemctl", "stop", "neoexit").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl stop: %v %s", err, out)
		}
	}
	s, err := config.Open(cfgPath)
	if err == nil {
		pass := config.RandomString(14)
		if err = s.Update(func(c *config.Config) error { c.Panel.SetPassword(pass); return nil }); err == nil {
			printLogin(s.Get(), pass, plain)
		}
	}
	if was {
		if out, serr := exec.Command("systemctl", "start", "neoexit").CombinedOutput(); serr != nil && err == nil {
			err = fmt.Errorf("systemctl start: %v %s", serr, out)
		}
	}
	return err
}

// updateNow installs the latest release and restarts the service.
func updateNow() error {
	if err := needRoot(); err != nil {
		return err
	}
	fmt.Printf("%s›%s checking for a new version (this is %s)\n", cAcc, c0, version)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	u := &update.Updater{Current: version}
	tag, err := u.Now(ctx)
	if err != nil {
		return err
	}
	if tag == "" {
		fmt.Printf("%s✓%s already the latest (%s)\n", cOK, c0, u.Status().Latest)
		return nil
	}
	fmt.Printf("%s✓%s installed %s (checksum verified)\n", cOK, c0, tag)
	if serviceInstalled() {
		if out, err := exec.Command("systemctl", "restart", "neoexit").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl restart: %v %s", err, out)
		}
		fmt.Printf("%s✓%s service restarted\n", cOK, c0)
	}
	return nil
}

func restartService() error {
	if err := needRoot(); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "restart", "neoexit").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart: %v %s", err, out)
	}
	fmt.Printf("%s✓%s service restarted\n", cOK, c0)
	return nil
}

func menu(dir, sock string, plain bool) {
	cfgPath := filepath.Join(dir, "config.json")
	in := bufio.NewReader(os.Stdin)
	ask := func(q string) string {
		fmt.Print(q)
		s, err := in.ReadString('\n')
		if err != nil && s == "" {
			fmt.Println()
			os.Exit(0)
		}
		return strings.TrimSpace(s)
	}
	yes := func(q string) bool {
		a := strings.ToLower(ask(q + " [y/N] "))
		return a == "y" || a == "yes"
	}
	for {
		state := cErr + "not installed" + c0
		if serviceInstalled() {
			state = cErr + "stopped" + c0
			if serviceActive() {
				state = cOK + "running" + c0
			}
		}
		fmt.Printf("\n%s◆ NeoExit%s %s   %s\n\n", cAcc, c0, version, state)
		fmt.Print(`  1) Update to the latest version
  2) Reset panel password
  3) Panel address
  4) Status
  5) Restart service
  6) Install / repair
  7) Uninstall
  0) Exit

`)
		var err error
		switch ask("Choose: ") {
		case "1":
			err = updateNow()
		case "2":
			if yes("Set a new random panel password?") {
				err = resetPassword(cfgPath, plain)
			}
		case "3":
			var s *config.Store
			if s, err = config.Open(cfgPath); err == nil {
				printLogin(s.Get(), "", plain)
			}
		case "4":
			if !serviceActive() {
				err = errors.New("the service is not running")
			} else {
				status(sock)
			}
		case "5":
			err = restartService()
		case "6":
			err = install(dir, "", false)
		case "7":
			if yes("Remove NeoExit from this server?") {
				keep := yes("Keep the settings (exits, password) for a later install?")
				if err = uninstall(dir, keep); err == nil {
					return
				}
			}
		case "0", "q", "exit":
			return
		default:
			continue
		}
		if err != nil {
			fmt.Printf("%s✗ %v%s\n", cErr, err, c0)
		}
		ask(cDim + "Press Enter to go back" + c0)
	}
}
