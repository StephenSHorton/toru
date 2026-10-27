package main

import (
	"net"
	"testing"
)

func TestAllowedPeer(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"203.0.114.1", true},
		{"2606:4700::1111", true},
		{"::ffff:8.8.8.8", true},

		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"127.0.0.1", false},
		{"127.8.9.10", false},
		{"169.254.169.254", false}, // cloud metadata
		{"100.64.0.1", false},
		{"100.96.12.34", false}, // Toru LAN overlay
		{"100.127.255.255", false},
		{"0.0.0.0", false},
		{"224.0.0.251", false},
		{"239.255.255.250", false},
		{"255.255.255.255", false},
		{"::1", false},
		{"::", false},
		{"fe80::1", false},
		{"fd00::1", false},
		{"64:ff9b::a00:1", false}, // NAT64 of 10.0.0.1
		{"2002:a00:1::1", false},  // 6to4 of 10.0.0.1
		{"::ffff:10.0.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"ff02::1", false},
	}
	for _, c := range cases {
		if got := allowedPeer(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("allowedPeer(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
	if allowedPeer(nil) {
		t.Error("allowedPeer(nil) = true")
	}
}

func TestOwnAddrsAllowSend(t *testing.T) {
	_, ifnet, _ := net.ParseCIDR("198.51.100.7/24") // an interface address on the box
	own := newOwnAddrs(net.ParseIP("203.0.113.10"), []net.Addr{
		&net.IPNet{IP: net.ParseIP("198.51.100.7"), Mask: ifnet.Mask},
	}, 49152, 65535)

	cases := []struct {
		addr string
		want bool
	}{
		{"8.8.8.8:3478", true},       // not ours
		{"203.0.113.10:50000", true}, // our relay range: peer relayed through us
		{"203.0.113.10:22", false},   // SSH on the relay
		{"203.0.113.10:3478", false}, // the TURN listener itself
		{"203.0.113.10:8443", false}, // credential endpoint
		{"198.51.100.7:65535", true}, // interface address, relay range
		{"198.51.100.7:80", false},   // interface address, other port
		{"[::ffff:203.0.113.10]:22", false},
	}
	for _, c := range cases {
		a, err := net.ResolveUDPAddr("udp", c.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := own.allowSend(a); got != c.want {
			t.Errorf("allowSend(%s) = %v, want %v", c.addr, got, c.want)
		}
	}
	if own.allowSend(&net.TCPAddr{IP: net.ParseIP("8.8.8.8"), Port: 1}) {
		t.Error("allowSend accepted a non-UDP address")
	}
}
