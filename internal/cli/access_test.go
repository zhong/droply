package cli

import (
	"testing"
	"time"
)

func TestParseDurationNever(t *testing.T) {
	d, err := parseDuration("never")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 87600 * time.Hour // 10 years
	if d != expected {
		t.Fatalf("expected %v, got %v", expected, d)
	}
}

func TestParseDurationRegular(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"1h", time.Hour},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
	}
	for _, tt := range tests {
		d, err := parseDuration(tt.input)
		if err != nil {
			t.Fatalf("parseDuration(%q): unexpected error: %v", tt.input, err)
		}
		if d != tt.expected {
			t.Fatalf("parseDuration(%q): expected %v, got %v", tt.input, tt.expected, d)
		}
	}
}

func TestFormatTTL(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{315360000, "never"},
		{400000000, "never"},
		{604800, "7d"},
		{86400, "1d"},
		{3600, "1h0m0s"},
		{90000, "25h0m0s"},
	}
	for _, tt := range tests {
		got := formatTTL(tt.seconds)
		if got != tt.expected {
			t.Fatalf("formatTTL(%v): expected %q, got %q", tt.seconds, tt.expected, got)
		}
	}
}

func TestBuildShareLine(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		password string
		ips      []any
		ttl      float64
		expected string
	}{
		{
			name:     "password with default expiry",
			url:      "https://alice.droplydoc.com/blog",
			password: "abc123xyz",
			ttl:      86400,
			expected: "https://alice.droplydoc.com/blog | Password: abc123xyz | Expires: 1d",
		},
		{
			name:     "password never expires",
			url:      "https://alice.droplydoc.com",
			password: "abc123xyz",
			ttl:      315360000,
			expected: "https://alice.droplydoc.com | Password: abc123xyz | Expires: never",
		},
		{
			name:     "ip only",
			url:      "https://alice.droplydoc.com",
			ips:      []any{"10.0.0.0/8"},
			expected: "https://alice.droplydoc.com | IP: 10.0.0.0/8",
		},
		{
			name:     "password and ip",
			url:      "https://alice.droplydoc.com/docs",
			password: "my-secret",
			ips:      []any{"10.0.0.0/8", "192.168.1.0/24"},
			ttl:      604800,
			expected: "https://alice.droplydoc.com/docs | Password: my-secret | IP: 10.0.0.0/8, 192.168.1.0/24 | Expires: 7d",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildShareLine(tt.url, tt.password, tt.ips, tt.ttl)
			if got != tt.expected {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", tt.expected, got)
			}
		})
	}
}

func TestBuildAccessURL(t *testing.T) {
	tests := []struct {
		name     string
		apiURL   string
		sub      string
		project  string
		expected string
	}{
		{
			name:     "subdomain only",
			apiURL:   "https://api.droplydoc.com",
			sub:      "alice",
			expected: "https://alice.droplydoc.com",
		},
		{
			name:     "subdomain with project",
			apiURL:   "https://api.droplydoc.com",
			sub:      "alice",
			project:  "blog",
			expected: "https://alice.droplydoc.com/blog",
		},
		{
			name:     "staging environment",
			apiURL:   "http://api.staging.droplydoc.com",
			sub:      "bob",
			project:  "docs",
			expected: "http://bob.staging.droplydoc.com/docs",
		},
		{
			name:     "localhost returns empty",
			apiURL:   "http://localhost:8080",
			sub:      "alice",
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAccessURL(tt.apiURL, tt.sub, tt.project)
			if got != tt.expected {
				t.Fatalf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
