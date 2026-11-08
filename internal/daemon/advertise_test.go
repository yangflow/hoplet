package daemon

import (
	"net"
	"testing"
)

func TestAdvertisable(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{"all interfaces", &net.TCPAddr{IP: net.IPv4zero, Port: 47801}, true},
		{"all interfaces v6", &net.TCPAddr{IP: net.IPv6zero, Port: 47801}, true},
		{"one interface", &net.TCPAddr{IP: net.IPv4(192, 0, 2, 7), Port: 47801}, true},
		{"loopback", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 47801}, false},
		{"loopback v6", &net.TCPAddr{IP: net.IPv6loopback, Port: 47801}, false},
		{"not tcp", &net.UnixAddr{Name: "x", Net: "unix"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := advertisable(tt.addr); got != tt.want {
				t.Fatalf("advertisable(%v) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
