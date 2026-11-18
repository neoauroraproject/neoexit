// NeoExit: free exits (WARP, the server's own IPv4/IPv6, any proxy) for
// AI services, tested all the time and switched on their own, offered as
// one local SOCKS5/HTTP port.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"neoexit/internal/config"
	"neoexit/internal/engine"
	"neoexit/internal/proxy"
	"neoexit/internal/update"
	"neoexit/internal/web"
)

var version = "1.3.0-dev"

func defaultDir() string {
	if runtime.GOOS == "linux" {
		return "/etc/neoexit"
	}
	return "data"
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "settings folder")
	plain := fs.Bool("http", false, "panel over plain HTTP (local testing only)")
	sock := fs.String("socket", web.SocketPath, "local API socket for NeoTunnel")
	fs.Parse(os.Args[2:])
	cfgPath := filepath.Join(*dir, "config.json")

	switch cmd {
	case "init":
		s, pass, err := config.Create(cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		printLogin(s.Get(), pass, *plain)
	case "run":
		run(*dir, cfgPath, *plain, *sock)
	case "password":
		s, err := config.Open(cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		pass := config.RandomString(14)
		s.Update(func(c *config.Config) error { c.Panel.SetPassword(pass); return nil })
		printLogin(s.Get(), pass, *plain)
	case "info":
		s, err := config.Open(cfgPath)
		if err != nil {
			log.Fatal(err)
		}
		printLogin(s.Get(), "", *plain)
	case "status":
		status(*sock)
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `neoexit <command>

  init      create the settings and print the panel address and password
  run       run the service (systemd does this)
  info      print the panel address
  password  set a new random panel password and print it
  status    print what the running service sees
  version   print the version
`)
}

// publicIP is the server's public IPv4, for the panel address. It asks over
// IPv4 only (so an IPv6-first server still prints its IPv4), falls back
// to a second service, then to the first public IPv4 on an interface.
func publicIP() string {
	d := &net.Dialer{Timeout: 5 * time.Second}
	c := &http.Client{Timeout: 7 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp4", addr) },
	}}
	for _, u := range []string{"https://www.cloudflare.com/cdn-cgi/trace", "https://api.ipify.org", "https://ipv4.icanhazip.com"} {
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		for _, line := range splitLines(string(b)) {
			line = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
			if ip := net.ParseIP(line); ip != nil && ip.To4() != nil {
				return ip.String()
			}
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsPrivate() && !n.IP.IsLinkLocalUnicast() {
				return n.IP.String()
			}
		}
	}
	return "SERVER-IPv4"
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func printLogin(c config.Config, pass string, plain bool) {
	scheme := "https"
	if plain {
		scheme = "http"
	}
	fmt.Printf("\n  panel     %s://%s:%d/%s/\n  username  %s\n", scheme, publicIP(), c.Panel.Port, c.Panel.Path, c.Panel.User)
	if pass != "" {
		fmt.Printf("  password  %s\n", pass)
	}
	fmt.Printf("  proxy     %s:%d (SOCKS5 and HTTP)\n\n", c.Proxy.Listen, c.Proxy.Port)
}

func run(dir, cfgPath string, plain bool, sock string) {
	store, err := config.Open(cfgPath)
	if os.IsNotExist(err) {
		var pass string
		store, pass, err = config.Create(cfgPath)
		if err == nil {
			printLogin(store.Get(), pass, plain)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng := engine.New(store, dir)
	px := &proxy.Server{Route: eng.Route}
	var pmu sync.Mutex
	restartProxy := func() error {
		pmu.Lock()
		defer pmu.Unlock()
		cfg := store.Get()
		px.Close()
		px.User, px.Pass = cfg.Proxy.User, cfg.Proxy.Pass
		return px.Listen(net.JoinHostPort(cfg.Proxy.Listen, strconv.Itoa(cfg.Proxy.Port)))
	}
	if err := restartProxy(); err != nil {
		log.Fatalf("proxy: %v", err)
	}
	// After an update, ending the service makes systemd start the new binary.
	upd := &update.Updater{Current: version, Restart: stop}
	upd.Start()
	app := &web.App{Store: store, Engine: eng, Proxy: px, Version: version, RestartProxy: restartProxy, Updater: upd}

	cfg := store.Get()
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Panel.Port),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		var err error
		if plain {
			err = srv.ListenAndServe()
		} else {
			cert, cerr := web.Certificate(dir)
			if cerr != nil {
				log.Fatalf("panel certificate: %v", cerr)
			}
			srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
			err = srv.ListenAndServeTLS("", "")
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("panel: %v", err)
		}
	}()
	if ln, err := web.ListenLocal(sock); err == nil {
		go http.Serve(ln, app.LocalHandler())
		defer os.Remove(sock)
	} else {
		log.Printf("local API off: %v", err)
	}
	log.Printf("neoexit %s: panel on :%d, proxy on %s:%d", version, cfg.Panel.Port, cfg.Proxy.Listen, cfg.Proxy.Port)
	eng.Run(ctx)
	eng.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
	px.Close()
}

func status(sock string) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}, Timeout: 5 * time.Second}
	resp, err := c.Get("http://neoexit/v1/status")
	if err != nil {
		log.Fatalf("service not running? %v", err)
	}
	defer resp.Body.Close()
	var s web.StatusV1
	json.NewDecoder(resp.Body).Decode(&s)
	fmt.Printf("NeoExit %s   proxy %s\n\n", s.Version, s.Proxy)
	for _, x := range s.Services {
		fmt.Printf("  %-12s %-10s via %s\n", x.Name, x.State, x.Exit)
	}
	fmt.Println()
	for _, x := range s.Exits {
		up := "down"
		if x.Up {
			up = "up"
		}
		fmt.Printf("  %-14s %-5s %-40s %s %dms\n", x.Name, up, x.IP, x.Country, x.Ms)
	}
}
