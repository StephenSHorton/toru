package share

import (
	"net"
	"sort"
)

// lanIPv4s returns this machine's usable IPv4 addresses, best candidate
// first. Loopback and link-local (169.254/16) are dropped. 192.168 is
// preferred over 10/8, which is preferred over other private ranges, so a
// Wi-Fi address wins over a Hyper-V or WSL adapter when both exist.
func lanIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip)
		}
	}
	sort.SliceStable(ips, func(i, j int) bool {
		return lanRank(ips[i]) < lanRank(ips[j])
	})
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}

func lanRank(ip net.IP) int {
	switch {
	case ip[0] == 192 && ip[1] == 168:
		return 0
	case ip[0] == 10:
		return 1
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return 2
	default:
		return 3
	}
}
