package routing

// NeoExit's own use of the lists: a proxy port with routing rules sends
// each category (Iran, ads, games...) where its owner chose, so 3x-ui
// can hand all its traffic to that port and keep no rules of its own.
//
// hmray.dat and hmray-geoip.dat are Xray's GeoSiteList and GeoIPList
// (protobuf); only the few fields needed are read, by hand.

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"

	"neoexit/internal/netguard"
)

// Category is a group of lists, the same ones the panel's Xray merge uses.
type Category struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Def   string   `json:"def"` // suggested target: "", direct, block
	Sites []string `json:"-"`   // hmray.dat lists
	IPs   []string `json:"-"`   // hmray-geoip.dat lists
	// Suffixes are extra domain suffixes (".ir" for Iran).
	Suffixes []string `json:"-"`
}

// Categories in the order their rules are checked; AI services are
// checked right after "block" and "torrent".
var Categories = []Category{
	{ID: "block", Name: "تبلیغات، بدافزار و فیشینگ", Def: "block", Sites: []string{"ads", "malware", "phishing"}},
	// torrent has no lists: it is found from the first bytes.
	{ID: "torrent", Name: "تورنت (BitTorrent)", Def: "block"},
	{ID: "iran", Name: "سایت‌ها و IPهای ایران", Def: "direct", Sites: []string{"iran"}, Suffixes: []string{"ir"},
		IPs: []string{"iran", "arvancloud", "derakcloud", "iranserver", "parspack"}},
	{ID: "sanctioned", Name: "سایت‌هایی که ایران را تحریم کرده‌اند", Sites: []string{"sanctioned"}},
	{ID: "gaming", Name: "بازی (Steam، Riot، Epic، کنسول‌ها…)", Sites: []string{"gaming"}, IPs: []string{"game-riot", "game-steam", "game-blizzard"}},
	{ID: "streaming", Name: "YouTube و استریم (Netflix، Spotify، Twitch…)", Sites: []string{"youtube", "streaming", "netflix"}, IPs: []string{"netflix"}},
	{ID: "messaging", Name: "پیام‌رسان‌ها (تلگرام، واتساپ، دیسکورد…)", Sites: []string{"messaging", "telegram", "discord"}, IPs: []string{"telegram"}},
	{ID: "developer", Name: "برنامه‌نویسی (GitHub، npm، Docker…)", Sites: []string{"github", "developer"}, IPs: []string{"github"}},
	{ID: "adult", Name: "بزرگسالان", Sites: []string{"adult"}},
}

// Preset is a ready rule the panel offers, written the way 3x-ui takes it.
type Preset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Target   string   `json:"target"` // suggested: "direct", "block" or ""
	Domains  []string `json:"domains,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
}

// Presets are the categories as rules, in the order they are best placed.
var Presets = func() []Preset {
	var out []Preset
	for _, c := range Categories {
		p := Preset{ID: c.ID, Name: c.Name, Target: c.Def}
		for _, s := range c.Sites {
			p.Domains = append(p.Domains, "ext:hmray.dat:"+s)
		}
		for _, s := range c.Suffixes {
			p.Domains = append(p.Domains, "domain:"+s)
		}
		for _, s := range c.IPs {
			p.IPs = append(p.IPs, "ext:hmray-geoip.dat:"+s)
		}
		if c.ID == "torrent" {
			p.Protocol = "bittorrent"
		}
		out = append(out, p)
	}
	return out
}()

// CategoryByID returns a category, or nil.
func CategoryByID(id string) *Category {
	for i := range Categories {
		if Categories[i].ID == id {
			return &Categories[i]
		}
	}
	return nil
}

// Matcher says whether a host name or IP is in a category.
type Matcher struct {
	full     map[string]bool // exact names
	root     map[string]bool // the name and everything under it
	keywords []string
	regexps  []*regexp.Regexp
	nets     []*net.IPNet
	subs     []*Matcher // lists it takes in whole (geosite:, ext:)
	private  bool       // geoip:private
}

func newMatcher() *Matcher { return &Matcher{full: map[string]bool{}, root: map[string]bool{}} }

func (m *Matcher) HasIPs() bool {
	if len(m.nets) > 0 || m.private {
		return true
	}
	for _, s := range m.subs {
		if s.HasIPs() {
			return true
		}
	}
	return false
}

func (m *Matcher) HasDomains() bool {
	if len(m.full)+len(m.root)+len(m.keywords)+len(m.regexps) > 0 {
		return true
	}
	for _, s := range m.subs {
		if s.HasDomains() {
			return true
		}
	}
	return false
}

// MatchHost checks a host name (lower case, no trailing dot).
func (m *Matcher) MatchHost(host string) bool {
	if m.full[host] {
		return true
	}
	for h := host; ; {
		if m.root[h] {
			return true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	for _, k := range m.keywords {
		if strings.Contains(host, k) {
			return true
		}
	}
	for _, r := range m.regexps {
		if r.MatchString(host) {
			return true
		}
	}
	for _, s := range m.subs {
		if s.MatchHost(host) {
			return true
		}
	}
	return false
}

func (m *Matcher) MatchIP(ip net.IP) bool {
	if m.private && netguard.IsPrivate(ip) {
		return true
	}
	for _, n := range m.nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, s := range m.subs {
		if s.MatchIP(ip) {
			return true
		}
	}
	return false
}

var (
	matchMu  sync.Mutex
	matchers = map[string]*Matcher{}
)

// For returns the matcher of a category, built on first use and kept.
func For(id string) (*Matcher, error) {
	matchMu.Lock()
	defer matchMu.Unlock()
	if m := matchers[id]; m != nil {
		return m, nil
	}
	c := CategoryByID(id)
	if c == nil {
		return nil, errors.New("unknown category " + id)
	}
	m := newMatcher()
	for _, s := range c.Suffixes {
		m.root[s] = true
	}
	want := map[string]bool{}
	for _, s := range c.Sites {
		want[strings.ToUpper(s)] = true
	}
	if err := readSites(siteDat, want, m); err != nil {
		return nil, err
	}
	want = map[string]bool{}
	for _, s := range c.IPs {
		want[strings.ToUpper(s)] = true
	}
	if err := readIPs(ipDat, want, m); err != nil {
		return nil, err
	}
	matchers[id] = m
	return m, nil
}

// --- protobuf, the little that is needed --------------------------------

var errShort = errors.New("routing list is cut short")

func varint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// fields calls f for each field of a message; for length-delimited ones
// data is the bytes, for varints it is nil and v the value.
func fields(b []byte, f func(num int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n := varint(b)
		if n == 0 {
			return errShort
		}
		b = b[n:]
		num, typ := int(key>>3), key&7
		switch typ {
		case 0:
			v, n := varint(b)
			if n == 0 {
				return errShort
			}
			b = b[n:]
			if err := f(num, v, nil); err != nil {
				return err
			}
		case 2:
			l, n := varint(b)
			if n == 0 || uint64(len(b)-n) < l {
				return errShort
			}
			data := b[n : n+int(l)]
			b = b[n+int(l):]
			if err := f(num, 0, data); err != nil {
				return err
			}
		case 1:
			if len(b) < 8 {
				return errShort
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return errShort
			}
			b = b[4:]
		default:
			return errors.New("routing list: unexpected field type")
		}
	}
	return nil
}

// code reads field 1 (country_code) of a GeoSite or GeoIP entry.
func code(entry []byte) string {
	c := ""
	fields(entry, func(num int, _ uint64, data []byte) error {
		if num == 1 && data != nil {
			c = strings.ToUpper(string(data))
			return errors.New("done")
		}
		return nil
	})
	return c
}

// GeoSiteList{1: GeoSite{1: code, 2: Domain{1: type, 2: value}}}.
// Domain types: 0 keyword, 1 regexp, 2 root domain, 3 full name.
func readSites(dat []byte, want map[string]bool, m *Matcher) error {
	if len(want) == 0 {
		return nil
	}
	return fields(dat, func(num int, _ uint64, entry []byte) error {
		if num != 1 || entry == nil || !want[code(entry)] {
			return nil
		}
		return fields(entry, func(num int, _ uint64, d []byte) error {
			if num != 2 || d == nil {
				return nil
			}
			typ, val := uint64(0), ""
			fields(d, func(num int, v uint64, data []byte) error {
				switch {
				case num == 1 && data == nil:
					typ = v
				case num == 2 && data != nil:
					val = strings.ToLower(string(data))
				}
				return nil
			})
			if val == "" {
				return nil
			}
			switch typ {
			case 0:
				m.keywords = append(m.keywords, val)
			case 1:
				if r, err := regexp.Compile(val); err == nil {
					m.regexps = append(m.regexps, r)
				}
			case 2:
				m.root[val] = true
			case 3:
				m.full[val] = true
			}
			return nil
		})
	})
}

// GeoIPList{1: GeoIP{1: code, 2: CIDR{1: ip, 2: prefix}}}.
func readIPs(dat []byte, want map[string]bool, m *Matcher) error {
	if len(want) == 0 {
		return nil
	}
	return fields(dat, func(num int, _ uint64, entry []byte) error {
		if num != 1 || entry == nil || !want[code(entry)] {
			return nil
		}
		return fields(entry, func(num int, _ uint64, c []byte) error {
			if num != 2 || c == nil {
				return nil
			}
			var ip []byte
			prefix := uint64(0)
			fields(c, func(num int, v uint64, data []byte) error {
				switch {
				case num == 1 && data != nil:
					ip = data
				case num == 2 && data == nil:
					prefix = v
				}
				return nil
			})
			if len(ip) != 4 && len(ip) != 16 {
				return nil
			}
			m.nets = append(m.nets, &net.IPNet{IP: net.IP(ip), Mask: net.CIDRMask(int(prefix), len(ip)*8)})
			return nil
		})
	})
}

// Codes lists the entries of both files (for tests and checks).
func Codes() (sites, ips []string) {
	for _, x := range []struct {
		dat []byte
		out *[]string
	}{{siteDat, &sites}, {ipDat, &ips}} {
		fields(x.dat, func(num int, _ uint64, entry []byte) error {
			if num == 1 && entry != nil {
				*x.out = append(*x.out, code(entry))
			}
			return nil
		})
	}
	return
}
