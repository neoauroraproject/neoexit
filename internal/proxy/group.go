package proxy

import (
	"fmt"
	"sync"

	"neoexit/internal/route"
)

// Spec is one extra proxy port.
type Spec struct {
	ID, Addr   string
	User, Pass string
	Route      Router
	Sniff      func() bool
	Count      func(route.Decision, int64)
}

// Group runs the extra proxy ports. Set replaces them all; connections
// already open keep going.
type Group struct {
	mu sync.Mutex
	on map[string]*Server
}

// Set closes the running ports and opens these. If one can't open, none
// stay open and the error names it.
func (g *Group) Set(specs []Spec) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.on {
		s.Close()
	}
	g.on = map[string]*Server{}
	for _, sp := range specs {
		s := &Server{ID: sp.ID, Route: sp.Route, User: sp.User, Pass: sp.Pass, Sniff: sp.Sniff, Count: sp.Count}
		if err := s.Listen(sp.Addr); err != nil {
			for _, o := range g.on {
				o.Close()
			}
			g.on = map[string]*Server{}
			return fmt.Errorf("%s: %w", sp.Addr, err)
		}
		g.on[sp.ID] = s
	}
	return nil
}

// Active is the number of open connections on one port.
func (g *Group) Active(id string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.on[id]; s != nil {
		return s.Active.Load()
	}
	return 0
}

// Running says whether a port is open.
func (g *Group) Running(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.on[id] != nil
}

func (g *Group) Close() { g.Set(nil) }
