// Package netguard keeps pact-core's two ports apart at the socket level. A
// container listening on a port is reachable from every network it has joined,
// so publishing the control plane to the host only is not enough on its own:
// the control-plane listener refuses connections that arrive on the agent,
// runner or backend networks, and the broker listener accepts only connections
// that arrive on the agent network.
package netguard

import (
	"fmt"
	"log"
	"net"
	"strings"
)

// Rules decides which local addresses a listener serves.
type Rules struct {
	Name  string
	Allow []*net.IPNet // if non-empty, the local address must be in one of these
	Deny  []*net.IPNet // the local address must not be in any of these
}

// ParseCIDRs parses a comma-separated list of CIDR blocks.
func ParseCIDRs(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("netguard: bad CIDR %q: %w", part, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// Permits reports whether a connection that arrived on local may be served.
func (r Rules) Permits(local net.IP) bool {
	if local.IsLoopback() {
		return len(r.Allow) == 0
	}
	for _, n := range r.Deny {
		if n.Contains(local) {
			return false
		}
	}
	if len(r.Allow) == 0 {
		return true
	}
	for _, n := range r.Allow {
		if n.Contains(local) {
			return true
		}
	}
	return false
}

type listener struct {
	net.Listener
	rules Rules
}

// Wrap returns a listener that closes connections the rules do not permit.
func Wrap(l net.Listener, rules Rules) net.Listener {
	if len(rules.Allow) == 0 && len(rules.Deny) == 0 {
		return l
	}
	return &listener{Listener: l, rules: rules}
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		tcp, ok := conn.LocalAddr().(*net.TCPAddr)
		if ok && l.rules.Permits(tcp.IP) {
			return conn, nil
		}
		log.Printf("netguard: refused %s connection from %s arriving on %s", l.rules.Name, conn.RemoteAddr(), conn.LocalAddr())
		_ = conn.Close()
	}
}
