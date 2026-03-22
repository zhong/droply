# Access Control CLI UX Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Optimize CLI access control UX with copy-friendly share output and `--expire never` support.

**Architecture:** Pure CLI formatting changes + one server-side validation tweak. No new APIs, no schema changes.

**Tech Stack:** Go, cobra CLI, chi router, Go testing

---

### Task 1: Raise server TTL upper bound

**Files:**
- Modify: `internal/server/access.go:149`
- Modify: `internal/server/access_test.go`

- [ ] **Step 1: Write failing test for large TTL acceptance**

Add to `internal/server/access_test.go`:

```go
func TestSetAccessLargeTTL(t *testing.T) {
	srv := newTestServer(t)
	token := registerAndGetToken(t, srv, "largettl@example.com", "password123")
	createSubdomain(t, srv, token, "largettl")

	body, _ := json.Marshal(map[string]interface{}{
		"auto_password": true,
		"session_ttl":   315360000, // 10 years
	})
	req := httptest.NewRequest(http.MethodPut, "/subdomains/largettl/access", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if int(resp["session_ttl"].(float64)) != 315360000 {
		t.Fatalf("expected session_ttl 315360000, got %v", resp["session_ttl"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestSetAccessLargeTTL -v`
Expected: FAIL — server returns 400 because current max is 2,592,000.

- [ ] **Step 3: Raise TTL upper bound in server**

In `internal/server/access.go:149`, change:

```go
// Before:
if ttl < 300 || ttl > 2592000 {
    jsonError(w, "session_ttl must be between 300 and 2592000", http.StatusBadRequest)

// After:
if ttl < 300 || ttl > 315360000 {
    jsonError(w, "session_ttl must be between 300 and 315360000", http.StatusBadRequest)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/server/ -run TestSetAccessLargeTTL -v`
Expected: PASS

- [ ] **Step 5: Run all server tests for regression**

Run: `go test ./internal/server/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/server/access.go internal/server/access_test.go
git commit -m "feat: raise TTL upper bound to 315360000 (10 years) for never-expire support"
```

---

### Task 2: Add CLI helpers — `parseDuration("never")`, `formatTTL`, `buildShareLine`

**Files:**
- Modify: `internal/cli/access.go`
- Create: `internal/cli/access_test.go`

- [ ] **Step 1: Write tests for `parseDuration("never")`**

Create `internal/cli/access_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify `parseDuration` tests — "never" should fail, regular should pass**

Run: `go test ./internal/cli/ -run TestParseDuration -v`
Expected: `TestParseDurationNever` FAIL, `TestParseDurationRegular` PASS.

- [ ] **Step 3: Implement `parseDuration("never")`**

In `internal/cli/access.go`, modify `parseDuration`:

```go
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "never") {
		return 87600 * time.Hour, nil // 10 years
	}
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/cli/ -run TestParseDuration -v`
Expected: All PASS.

- [ ] **Step 5: Write tests for `formatTTL`**

Add to `internal/cli/access_test.go`:

```go
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
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestFormatTTL -v`
Expected: FAIL — `formatTTL` not defined.

- [ ] **Step 7: Implement `formatTTL`**

Add to `internal/cli/access.go`:

```go
func formatTTL(seconds float64) string {
	s := int(seconds)
	if s >= 315360000 {
		return "never"
	}
	if s >= 86400 && s%86400 == 0 {
		return fmt.Sprintf("%dd", s/86400)
	}
	return (time.Duration(s) * time.Second).String()
}
```

- [ ] **Step 8: Run test to verify pass**

Run: `go test ./internal/cli/ -run TestFormatTTL -v`
Expected: All PASS.

- [ ] **Step 9: Write tests for `buildShareLine`**

Add to `internal/cli/access_test.go`:

```go
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
```

- [ ] **Step 10: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestBuildShareLine -v`
Expected: FAIL — `buildShareLine` not defined.

- [ ] **Step 11: Implement `buildShareLine`**

Add to `internal/cli/access.go`:

```go
func buildShareLine(url, password string, ips []any, ttlSeconds float64) string {
	parts := []string{url}
	if password != "" {
		parts = append(parts, "Password: "+password)
	}
	if len(ips) > 0 {
		ipStrs := make([]string, len(ips))
		for i, ip := range ips {
			ipStrs[i] = fmt.Sprintf("%v", ip)
		}
		parts = append(parts, "IP: "+strings.Join(ipStrs, ", "))
	}
	if password != "" && ttlSeconds > 0 {
		parts = append(parts, "Expires: "+formatTTL(ttlSeconds))
	}
	return strings.Join(parts, " | ")
}
```

- [ ] **Step 12: Run test to verify pass**

Run: `go test ./internal/cli/ -run TestBuildShareLine -v`
Expected: All PASS.

- [ ] **Step 13: Commit**

```bash
git add internal/cli/access.go internal/cli/access_test.go
git commit -m "feat: add parseDuration never, formatTTL, and buildShareLine helpers"
```

---

### Task 3: Add `buildAccessURL` helper

**Files:**
- Modify: `internal/cli/access.go`
- Modify: `internal/cli/access_test.go`

- [ ] **Step 1: Write test for `buildAccessURL`**

Add to `internal/cli/access_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestBuildAccessURL -v`
Expected: FAIL — `buildAccessURL` not defined.

- [ ] **Step 3: Implement `buildAccessURL`**

Add to `internal/cli/access.go` (add `"net/url"` to the existing import block):

```go
func buildAccessURL(apiURL, subdomain, project string) string {
	u, err := url.Parse(apiURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if !strings.HasPrefix(host, "api.") {
		return ""
	}
	siteHost := strings.TrimPrefix(host, "api.")
	siteHost = subdomain + "." + siteHost
	if port := u.Port(); port != "" {
		siteHost += ":" + port
	}
	result := u.Scheme + "://" + siteHost
	if project != "" {
		result += "/" + project
	}
	return result
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/cli/ -run TestBuildAccessURL -v`
Expected: All PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/access.go internal/cli/access_test.go
git commit -m "feat: add buildAccessURL helper for share line URL construction"
```

---

### Task 4: Update `access set` output to print share line

**Files:**
- Modify: `internal/cli/access.go:68-81`

- [ ] **Step 1: Replace `access set` output block**

Replace the output section in `newAccessSetCmd` (lines 68-81) with:

```go
			fmt.Println("Access control updated.")

			// Determine password to display.
			var displayPassword string
			if gp, ok := result["generated_password"].(string); ok && gp != "" {
				displayPassword = gp
			} else if password != "" && password != "auto" {
				displayPassword = password
			}

			// Build share line if site URL can be derived.
			siteURL := buildAccessURL(cfg.APIURL, sub, project)
			if siteURL != "" {
				var ips []any
				if ipList, ok := result["allowed_ips"].([]any); ok {
					ips = ipList
				}

				var ttl float64
				if t, ok := result["session_ttl"].(float64); ok {
					ttl = t
				}

				fmt.Println(buildShareLine(siteURL, displayPassword, ips, ttl))
			} else {
				// Fallback: original format for non-standard API URLs.
				if ips := result["allowed_ips"]; ips != nil {
					fmt.Printf("  IP whitelist: %v\n", ips)
				}
				if displayPassword != "" {
					fmt.Printf("  Password: %s\n", displayPassword)
				} else if result["has_password"] == true {
					fmt.Println("  Password: (set)")
				}
				if ttl, ok := result["session_ttl"].(float64); ok {
					fmt.Printf("  Session TTL: %s\n", formatTTL(ttl))
				}
			}
```

- [ ] **Step 2: Run all CLI tests**

Run: `go test ./internal/cli/ -v`
Expected: All PASS.

- [ ] **Step 3: Build to verify compilation**

Run: `go build ./...`
Expected: Success, no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/access.go
git commit -m "feat: access set outputs copy-friendly share line"
```

---

### Task 5: Update `access get` output to use friendly TTL

**Files:**
- Modify: `internal/cli/access.go:125-134`

- [ ] **Step 1: Update `access get` output**

Replace the TTL display line in `newAccessGetCmd` (lines 132-133) with friendly formatting. Also suppress TTL for IP-only rules:

```go
			fmt.Printf("Access control for %s:\n", target)
			if ips := result["allowed_ips"]; ips != nil {
				fmt.Printf("  IP whitelist: %v\n", ips)
			}
			if result["has_password"] == true {
				fmt.Println("  Password: (set)")
			}
			if result["has_password"] == true {
				if ttl, ok := result["session_ttl"].(float64); ok {
					fmt.Printf("  Session TTL: %s\n", formatTTL(ttl))
				}
			}
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./...`
Expected: Success, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/access.go
git commit -m "feat: access get uses friendly TTL display, suppresses TTL for IP-only rules"
```

---

### Task 6: Update `--expire` flag description

**Files:**
- Modify: `internal/cli/access.go:91`

- [ ] **Step 1: Update flag help text**

Change line 91:

```go
// Before:
cmd.Flags().String("expire", "24h", "Session expiry duration (e.g. 1h, 24h, 7d)")

// After:
cmd.Flags().String("expire", "24h", "Session expiry duration (e.g. 1h, 24h, 7d, never)")
```

- [ ] **Step 2: Commit**

```bash
git add internal/cli/access.go
git commit -m "docs: add 'never' to --expire flag help text"
```

---

### Task 7: Final verification

- [ ] **Step 1: Run all tests**

Run: `go test ./... -v`
Expected: All PASS.

- [ ] **Step 2: Build binary**

Run: `go build ./...`
Expected: Success.
