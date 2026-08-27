package main

import "testing"

func TestRedactIP(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"valid IPv4", "192.168.1.10", "192.168.1.x"},
		{"valid IPv4 loopback", "127.0.0.1", "127.0.0.x"},
		{"valid IPv6 full", "[2001:db8::1]", "2001:db8::x"},
		{"valid IPv6 loopback", "::1", "::x"},
		{"invalid IP string", "not-an-ip", ""},
		{"empty string", "", ""},
		{"malformed IPv4 extra octet", "192.168.1.1.1", ""},
		{"IPv4-mapped IPv6", "[::ffff:192.168.1.10]", "192.168.1.x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactIP(tc.input)
			if got != tc.want {
				t.Errorf("redactIP(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
