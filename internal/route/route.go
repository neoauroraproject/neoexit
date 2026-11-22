// Package route holds what a proxy asks its router and what it gets back,
// shared by the proxy (which asks) and the engine (which decides).
package route

// Request is one connection or UDP destination.
type Request struct {
	Host    string // a name or an IP, as the client asked
	Port    int
	Network string // "tcp" or "udp"
	// Sniffed is the name found in the first bytes (TLS SNI, HTTP Host)
	// when the client asked for an IP.
	Sniffed string
	// Protocol found in the first bytes: "http", "tls", "quic",
	// "bittorrent" or "".
	Protocol string
	// Proxy is the NeoExit proxy it came in on: "main" or an extra
	// proxy's ID (Xray's inboundTag).
	Proxy string
}

// Decision is where a request goes and why.
type Decision struct {
	// Target: an exit ID, "direct" (the server's route) or "block".
	Target string `json:"target"`
	// Rule: "private", "exit", "service:<id>" (or "service:ai"),
	// "rule:<id>" or "default".
	Rule string `json:"rule"`
}
