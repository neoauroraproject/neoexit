package routing

// Lists for rules, written exactly the way 3x-ui (Xray) takes them:
//
//	domains: x (anywhere in the name), domain:x (x and below), full:x,
//	         keyword:x, regexp:x, geosite:x, ext:file.dat:x
//	IPs:     1.2.3.4, 1.2.3.0/24, geoip:x, geoip:private, ext:file.dat:x
//
// hmray.dat and hmray-geoip.dat are built in. Other files are read from
// GeoDirs (/etc/neoexit/geo, then 3x-ui's folder, where geosite.dat and
// geoip.dat usually are); geosite:/geoip: fall back to the HMray lists
// when those files aren't there.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// GeoDirs are where ext: files are looked for.
var GeoDirs = func() []string {
	dirs := []string{"/etc/neoexit/geo"}
	if d := XUIDir(); d != "" {
		dirs = append(dirs, d)
	}
	return dirs
}

var (
	listMu sync.Mutex
	lists  = map[string]*Matcher{}
)

// list returns one entry (code) of a list file, kept once read.
func list(file string, site bool, code string) (*Matcher, error) {
	key := fmt.Sprintf("%s|%v|%s", file, site, strings.ToUpper(code))
	listMu.Lock()
	defer listMu.Unlock()
	if m := lists[key]; m != nil {
		return m, nil
	}
	var dat []byte
	switch file {
	case "hmray.dat":
		dat = siteDat
	case "hmray-geoip.dat":
		dat = ipDat
	default:
		if file == "" || file != filepath.Base(file) || !strings.HasSuffix(file, ".dat") {
			return nil, fmt.Errorf("%q is not a .dat file name", file)
		}
		for _, d := range GeoDirs() {
			if b, err := os.ReadFile(filepath.Join(d, file)); err == nil {
				dat = b
				break
			}
		}
		if dat == nil {
			return nil, fmt.Errorf("%s was not found in %s", file, strings.Join(GeoDirs(), " or "))
		}
	}
	m := newMatcher()
	want := map[string]bool{strings.ToUpper(code): true}
	var err error
	if site {
		err = readSites(dat, want, m)
	} else {
		err = readIPs(dat, want, m)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if !m.HasDomains() && !m.HasIPs() {
		return nil, fmt.Errorf("%s has no list %q", file, code)
	}
	lists[key] = m
	return m, nil
}

// Build makes a matcher from a rule's domains and IPs.
func Build(domains, ips []string) (*Matcher, error) {
	m := newMatcher()
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		kind, val, ok := strings.Cut(d, ":")
		if !ok {
			kind, val = "keyword", d // Xray: a plain string is a substring
		}
		val = strings.TrimSpace(val)
		switch kind {
		case "dotless":
			r, _ := regexp.Compile(`^[^.]*$`)
			if val != "" {
				r, _ = regexp.Compile(`^[^.]*` + regexp.QuoteMeta(val) + `[^.]*$`)
			}
			m.regexps = append(m.regexps, r)
		case "domain":
			m.root[strings.Trim(strings.ToLower(val), ".")] = true
		case "full":
			m.full[strings.ToLower(val)] = true
		case "keyword":
			m.keywords = append(m.keywords, strings.ToLower(val))
		case "regexp":
			r, err := regexp.Compile(val)
			if err != nil {
				return nil, fmt.Errorf("regexp %q: %v", val, err)
			}
			m.regexps = append(m.regexps, r)
		case "geosite":
			s, err := list("geosite.dat", true, val)
			if err != nil {
				if s, err = list("hmray.dat", true, val); err != nil {
					return nil, fmt.Errorf("geosite:%s is in neither geosite.dat nor hmray.dat", val)
				}
			}
			m.subs = append(m.subs, s)
		case "ext":
			file, code, ok := strings.Cut(val, ":")
			if !ok {
				return nil, fmt.Errorf("%q: write ext:file.dat:list", d)
			}
			s, err := list(file, true, code)
			if err != nil {
				return nil, err
			}
			m.subs = append(m.subs, s)
		default:
			return nil, fmt.Errorf("%q: unknown kind %q", d, kind)
		}
	}
	for _, x := range ips {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		switch {
		case x == "geoip:private":
			m.private = true
		case strings.HasPrefix(x, "geoip:"):
			code := strings.TrimPrefix(x, "geoip:")
			s, err := list("geoip.dat", false, code)
			if err != nil {
				if s, err = list("hmray-geoip.dat", false, code); err != nil {
					return nil, fmt.Errorf("geoip:%s is in neither geoip.dat nor hmray-geoip.dat", code)
				}
			}
			m.subs = append(m.subs, s)
		case strings.HasPrefix(x, "ext:"):
			file, code, ok := strings.Cut(strings.TrimPrefix(x, "ext:"), ":")
			if !ok {
				return nil, fmt.Errorf("%q: write ext:file.dat:list", x)
			}
			s, err := list(file, false, code)
			if err != nil {
				return nil, err
			}
			m.subs = append(m.subs, s)
		default:
			if _, n, err := net.ParseCIDR(x); err == nil {
				m.nets = append(m.nets, n)
			} else if ip := net.ParseIP(x); ip != nil {
				bits := 128
				if v4 := ip.To4(); v4 != nil {
					ip, bits = v4, 32
				}
				m.nets = append(m.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			} else {
				return nil, fmt.Errorf("%q is not an IP, CIDR, geoip: or ext: list", x)
			}
		}
	}
	return m, nil
}
