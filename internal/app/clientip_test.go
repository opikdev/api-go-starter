package app

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := map[string]struct {
		hops      int
		forwarded []string
		want      string
	}{
		"ignores a spoofed header with no trusted proxy": {0, []string{"203.0.113.9"}, "192.0.2.1"},
		"takes the last entry behind one proxy":          {1, []string{"203.0.113.9, 198.51.100.7"}, "198.51.100.7"},
		"takes the second from the right behind two":     {2, []string{"203.0.113.9, 198.51.100.7, 198.51.100.8"}, "198.51.100.7"},
		"joins repeated headers":                         {2, []string{"203.0.113.9", "198.51.100.7"}, "203.0.113.9"},
		"takes the leftmost when there are fewer hops":   {3, []string{"203.0.113.9, 198.51.100.7"}, "203.0.113.9"},
		"trims spaces and normalizes IPv6":               {1, []string{" 2001:DB8:0:0::1 "}, "2001:db8::1"},
		"falls back when the entry is empty":             {1, []string{"203.0.113.9, "}, "192.0.2.1"},
		"falls back when the entry isn't an address":     {1, []string{"unknown"}, "192.0.2.1"},
		"falls back when there's no header":              {1, nil, "192.0.2.1"},
	}
	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/notes", nil)
			r.RemoteAddr = "192.0.2.1:54321"
			for _, value := range c.forwarded {
				r.Header.Add("X-Forwarded-For", value)
			}
			if got := clientIP(r, c.hops); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRateLimitClient(t *testing.T) {
	for address, want := range map[string]string{
		"203.0.113.9":          "203.0.113.9",
		"::ffff:203.0.113.9":   "203.0.113.9",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"fe80::1%eth0":         "fe80::/64",
		"not an address":       "not an address",
	} {
		if got := rateLimitClient(address); got != want {
			t.Errorf("rateLimitClient(%q) = %q, want %q", address, got, want)
		}
	}
}
