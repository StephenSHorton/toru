package share

import (
	"net"
	"testing"
)

func TestLanRankPrefersHomeWiFi(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("172.22.0.1").To4(),
		net.ParseIP("10.0.0.4").To4(),
		net.ParseIP("192.168.1.20").To4(),
		net.ParseIP("100.64.0.2").To4(),
	}
	// Sort the way lanIPv4s does, via rank only.
	if lanRank(ips[2]) >= lanRank(ips[1]) || lanRank(ips[1]) >= lanRank(ips[0]) || lanRank(ips[0]) >= lanRank(ips[3]) {
		t.Fatalf("ranks = %d %d %d %d", lanRank(ips[2]), lanRank(ips[1]), lanRank(ips[0]), lanRank(ips[3]))
	}
}
