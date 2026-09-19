package core

import (
	"strings"
	"testing"
)

func TestClientConnectionReportValidation(t *testing.T) {
	valid := ClientConnection{Engine: EngineXray, Protocol: "vless", Inbound: "entry", Transport: "tcp", ClientIP: "2001:db8::2", ClientPort: 50123, LocalIP: "2001:db8::1", LocalPort: 443}
	report := ClientConnectionReport{Status: "ok", Connections: []ClientConnection{valid}}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ClientConnection){func(c *ClientConnection) { c.ClientIP = "hostname" }, func(c *ClientConnection) { c.ClientIP = "fe80::1%eth0" }, func(c *ClientConnection) { c.LocalPort = 65536 }, func(c *ClientConnection) { c.ClientPort = 0 }, func(c *ClientConnection) { c.Engine = "other" }, func(c *ClientConnection) { c.Inbound = "bad\x00" }, func(c *ClientConnection) { c.Transport = "quic" }, func(c *ClientConnection) { c.Protocol = strings.Repeat("p", 41) }} {
		c := valid
		mutate(&c)
		report.Connections = []ClientConnection{c}
		if report.Validate() == nil {
			t.Fatalf("accepted invalid connection: %+v", c)
		}
	}
	report.Connections = make([]ClientConnection, MaxClientConnections+1)
	if report.Validate() == nil {
		t.Fatal("accepted oversized report")
	}
	report.Connections = []ClientConnection{valid}
	report.Status = "unavailable"
	if report.Validate() == nil {
		t.Fatal("unavailable sample has connections")
	}
}
