package netguard

import (
	"net"
	"testing"
)

func TestRules(t *testing.T) {
	agent, _ := ParseCIDRs("172.31.10.0/24")
	internal, _ := ParseCIDRs("172.31.10.0/24, 172.31.20.0/24,172.31.30.0/24")
	broker := Rules{Name: "broker", Allow: agent}
	control := Rules{Name: "control", Deny: internal}

	cases := []struct {
		ip              string
		broker, control bool
	}{
		{"172.31.10.2", true, false},  // arrived on agent_net
		{"172.31.20.2", false, false}, // backend_net
		{"172.31.30.2", false, false}, // runner_net
		{"172.31.40.2", false, true},  // egress_net: where host-published traffic lands
		{"127.0.0.1", false, true},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := broker.Permits(ip); got != c.broker {
			t.Errorf("broker port, local %s: got %v want %v", c.ip, got, c.broker)
		}
		if got := control.Permits(ip); got != c.control {
			t.Errorf("control port, local %s: got %v want %v", c.ip, got, c.control)
		}
	}
	if _, err := ParseCIDRs("not-a-cidr"); err == nil {
		t.Error("bad CIDR accepted")
	}
}
