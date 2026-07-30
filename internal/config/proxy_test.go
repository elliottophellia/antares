package config

import "testing"

func TestParseProxyLine(t *testing.T) {
	ok := []struct {
		in                     string
		scheme, host, user, pw string
		port                   int
	}{
		{"proxy.geonode.io:9000:user:pass", "http", "proxy.geonode.io", "user", "pass", 9000},
		{"http://user:pass@host.com:8080", "http", "host.com", "user", "pass", 8080},
		{"socks5://host.com:1080", "socks5", "host.com", "", "", 1080},
		{"host.com:3128", "http", "host.com", "", "", 3128},
		{"user:pass@host.com:8080", "http", "host.com", "user", "pass", 8080},
		{"Label = http://u:p@h.io:9000", "http", "h.io", "u", "p", 9000},
		{"1.2.3.4:8000:onlyuser", "http", "1.2.3.4", "onlyuser", "", 8000},
	}
	for _, c := range ok {
		e, err := ParseProxyLine(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if e.Scheme != c.scheme || e.Host != c.host || e.Port != c.port || e.Username != c.user || e.Password != c.pw {
			t.Errorf("%q: got scheme=%s host=%s port=%d user=%q pw=%q", c.in, e.Scheme, e.Host, e.Port, e.Username, e.Password)
		}
	}

	for _, bad := range []string{"", "garbage-no-port", "host-only", "http://:0"} {
		if _, err := ParseProxyLine(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
