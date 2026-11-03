package lanaddr_test

import (
	"net"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/lanaddr"
)

func addr(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("parse %q: %v", cidr, err)
	}
	return &net.IPNet{IP: ip, Mask: network.Mask}
}

func TestPickPrefersTheRoutedLANAddress(t *testing.T) {
	got := lanaddr.Pick(
		net.ParseIP("192.168.3.22"),
		[]net.Addr{addr(t, "10.0.0.5/24"), addr(t, "172.17.0.1/16")},
	)
	if got != "192.168.3.22" {
		t.Fatalf("address = %q, want the routed LAN address", got)
	}
}

func TestPickIgnoresLoopbackAndLinkLocalAddresses(t *testing.T) {
	got := lanaddr.Pick(
		net.ParseIP("127.0.0.1"),
		[]net.Addr{
			addr(t, "127.0.0.1/8"),
			addr(t, "169.254.10.20/16"),
			addr(t, "::1/128"),
			addr(t, "fe80::1/64"),
			addr(t, "192.168.1.44/24"),
		},
	)
	if got != "192.168.1.44" {
		t.Fatalf("address = %q, want the usable IPv4 address", got)
	}
}

func TestPickPrefersTheHomeNetworkOverVirtualBridges(t *testing.T) {
	got := lanaddr.Pick(nil, []net.Addr{
		addr(t, "172.17.0.1/16"),
		addr(t, "10.0.0.7/24"),
		addr(t, "192.168.1.44/24"),
	})
	if got != "192.168.1.44" {
		t.Fatalf("address = %q, want the 192.168 network", got)
	}
}

func TestPickFallsBackToANonPrivateAddress(t *testing.T) {
	got := lanaddr.Pick(nil, []net.Addr{addr(t, "203.0.113.9/24")})
	if got != "203.0.113.9" {
		t.Fatalf("address = %q, want the only usable address", got)
	}
}

func TestPickReturnsNothingWithoutAUsableAddress(t *testing.T) {
	got := lanaddr.Pick(net.ParseIP("127.0.0.1"), []net.Addr{
		addr(t, "127.0.0.1/8"),
		addr(t, "169.254.10.20/16"),
		addr(t, "0.0.0.0/0"),
		addr(t, "::1/128"),
	})
	if got != "" {
		t.Fatalf("address = %q, want none", got)
	}
}
