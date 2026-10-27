package adapter

import (
	"net/netip"
	"strings"
	"testing"
)

func TestFindOverlap(t *testing.T) {
	p := netip.MustParsePrefix
	cases := []struct {
		name   string
		routes []Route
		want   string // prefix of the hit, "" for none
	}{
		{"empty", nil, ""},
		{"default route ignored", []Route{{Prefix: p("0.0.0.0/0"), Alias: "Ethernet"}}, ""},
		{"home LAN", []Route{{Prefix: p("192.168.1.0/24")}, {Prefix: p("192.168.1.255/32")}}, ""},
		{"broadcast and multicast", []Route{{Prefix: p("255.255.255.255/32")}, {Prefix: p("224.0.0.0/4")}}, ""},
		{"radmin 26/8", []Route{{Prefix: p("26.0.0.0/8"), Alias: "Radmin VPN"}}, ""},
		{"tailscale 100.64/10", []Route{{Prefix: p("100.64.0.0/10"), Alias: "Tailscale"}}, "100.64.0.0/10"},
		{"inside the /16", []Route{{Prefix: p("100.96.4.0/24"), Alias: "Other"}}, "100.96.4.0/24"},
		{"host route inside", []Route{{Prefix: p("100.96.12.34/32")}}, "100.96.12.34/32"},
		{"ours ignored", []Route{{Prefix: p("100.96.0.0/16"), Ours: true}}, ""},
		{"neighbour /16", []Route{{Prefix: p("100.97.0.0/16")}}, ""},
		{"ipv6 ignored", []Route{{Prefix: p("fd00::/8")}}, ""},
	}
	for _, c := range cases {
		r, ok := findOverlap(Overlay, c.routes)
		got := ""
		if ok {
			got = r.Prefix.String()
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestValidAddr(t *testing.T) {
	for s, want := range map[string]bool{
		"100.96.12.34":    true,
		"100.96.0.2":      true,
		"100.96.255.254":  true,
		"100.96.0.0":      false,
		"100.96.0.1":      false,
		"100.96.255.255":  false,
		"100.97.0.5":      false,
		"10.0.0.1":        false,
		"::ffff:100.96.1": false,
	} {
		a, err := netip.ParseAddr(s)
		if err != nil {
			if want {
				t.Fatalf("parse %s: %v", s, err)
			}
			continue
		}
		if got := ValidAddr(a); got != want {
			t.Errorf("ValidAddr(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestRangeErrorNamesAdapter(t *testing.T) {
	e := &RangeInUseError{Prefix: netip.MustParsePrefix("100.64.0.0/10"), Alias: "Tailscale"}
	if got := e.Error(); got == "" || !strings.Contains(got, "Tailscale") {
		t.Fatalf("error %q does not name the adapter", got)
	}
}
