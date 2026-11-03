// Package lanaddr finds the IPv4 address another machine on the local network
// can use to reach this computer. The Settings window shows it as the Server's
// connect address while LAN exposure is on, so the value is discovered from the
// operating system rather than configured by hand.
package lanaddr

import (
	"net"
	"sort"
)

// Address returns the preferred local-network IPv4 address of this computer,
// or "" when it has none.
func Address() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		addrs = nil
	}
	return Pick(outboundIP(), addrs)
}

// outboundIP asks the routing table which source address off-machine traffic
// would use. A UDP connect sends no packets, so this works offline; it fails
// only when there is no route at all.
func outboundIP() net.IP {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return nil
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP
	}
	return nil
}

// Pick chooses the address another LAN machine should use. The routed address
// wins when it is a usable private IPv4 address; otherwise the first suitable
// interface address wins. Loopback, link-local, multicast, and unspecified
// addresses are never returned.
func Pick(outbound net.IP, addrs []net.Addr) string {
	if ip := usableIPv4(outbound); ip != nil && ip.IsPrivate() {
		return ip.String()
	}
	candidates := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if usable := usableIPv4(ip); usable != nil {
			candidates = append(candidates, usable)
		}
	}
	// A home LAN usually uses 192.168/16, while Docker, WSL, and hypervisor
	// bridges commonly sit on 172.16/12; prefer the address a user would
	// recognise when the routing table gives no answer.
	sort.SliceStable(candidates, func(i, j int) bool {
		return lanRank(candidates[i]) < lanRank(candidates[j])
	})
	for _, ip := range candidates {
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	if ip := usableIPv4(outbound); ip != nil {
		return ip.String()
	}
	if len(candidates) > 0 {
		return candidates[0].String()
	}
	return ""
}

// usableIPv4 returns the IPv4 form of ip, or nil when ip cannot be a LAN
// address.
func usableIPv4(ip net.IP) net.IP {
	v4 := ip.To4()
	if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() || v4.IsMulticast() || v4.IsUnspecified() {
		return nil
	}
	return v4
}

// lanRank orders private ranges so a familiar home-LAN address wins over the
// virtual bridges that also use 172.16/12.
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
