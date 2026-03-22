# Cookie Invalidation on Password Change — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a user updates the access control password, all existing browser cookies are automatically invalidated, forcing re-authentication.

**Architecture:** Include `password_hash` in the HMAC payload used for cookie signing/verification. The cookie format visible to browsers is unchanged. When a subdomain-scoped cookie is validated against a project-level rule, the function looks up the subdomain-level rule's hash automatically.

**Tech Stack:** Go, HMAC-SHA256, bcrypt, SQLite

**Spec:** `docs/superpowers/specs/2026-03-22-cookie-invalidation-on-password-change-design.md`

---

### Task 1: Update `signCookie()` to include password hash in HMAC

**Files:**
- Modify: `internal/server/site.go:378-389` — `signCookie` function

- [ ] **Step 1: Update `signCookie` signature and HMAC payload**

Change `signCookie` to accept `passwordHash` and include it in the HMAC payload. The `passwordHash` participates only in the HMAC computation — the cookie value format remains `{subdomain}:{project}:{expiry}:{hmac}`:

```go
// signCookie creates a signed cookie value in the format:
// {subdomain}:{project_or_empty}:{expiry_unix}:{hmac_sha256_hex}
// The passwordHash is included in the HMAC payload (not in the cookie value)
// so that changing the password automatically invalidates existing cookies.
func (s *Server) signCookie(subdomain, project string, expiry time.Time, passwordHash string) string {
	expiryStr := strconv.FormatInt(expiry.Unix(), 10)
	payload := fmt.Sprintf("%s:%s:%s:%s", subdomain, project, expiryStr, passwordHash)

	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s:%s:%s:%s", subdomain, project, expiryStr, sig)
}
```

- [ ] **Step 2: Verify compilation fails (callers not updated yet)**

Run: `cd /Users/chenzhong/Developer/droply && go build ./...`
Expected: compilation errors in `siteLoginHandler` (line 322) calling `signCookie` with wrong number of arguments.

---

### Task 2: Update `isValidAccessCookie()` to verify with password hash

**Files:**
- Modify: `internal/server/site.go:391-438` — `isValidAccessCookie` function

- [ ] **Step 1: Update `isValidAccessCookie` to accept and use password hash**

```go
// isValidAccessCookie checks the _droply_access cookie for validity.
// passwordHash is from the rule returned by FindAccessRuleForSite.
// For subdomain-scoped cookies (cookieProj=="") where the passed rule is project-level,
// the function looks up the subdomain-level rule to get the correct hash.
func (s *Server) isValidAccessCookie(r *http.Request, subdomain, project string, isCustomDomain bool, passwordHash string) bool {
	cookie, err := r.Cookie("_droply_access")
	if err != nil {
		return false
	}

	parts := strings.SplitN(cookie.Value, ":", 4)
	if len(parts) != 4 {
		return false
	}

	cookieSub := parts[0]
	cookieProj := parts[1]
	expiryStr := parts[2]
	sig := parts[3]

	// Check expiry first (cheap, no DB needed).
	expiryUnix, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiryUnix {
		return false
	}

	// Check scope: cookie subdomain must match.
	if cookieSub != subdomain {
		return false
	}

	// Determine the correct passwordHash for HMAC verification.
	hashForVerify := passwordHash
	if cookieProj == "" && project != "" {
		// Subdomain-scoped cookie but we were given a project-level rule's hash.
		// Look up the subdomain-level rule to get the correct hash.
		subRule, err := s.store.FindAccessRuleForSite(cookieSub, "")
		if err != nil || subRule == nil {
			return false
		}
		hashForVerify = subRule.PasswordHash
	}

	// Verify HMAC with passwordHash included in payload.
	payload := fmt.Sprintf("%s:%s:%s:%s", cookieSub, cookieProj, expiryStr, hashForVerify)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return false
	}

	// Subdomain-level cookie (empty project) grants access to all projects.
	if cookieProj == "" {
		return true
	}

	// Project-level cookie must match the requested project.
	return cookieProj == project
}
```

- [ ] **Step 2: Verify compilation still fails (callers not updated yet)**

Run: `cd /Users/chenzhong/Developer/droply && go build ./...`
Expected: compilation errors — callers pass wrong number of arguments.

---

### Task 3: Update callers — `siteHandler` and `siteLoginHandler`

**Files:**
- Modify: `internal/server/site.go:208` — `siteHandler` call to `isValidAccessCookie`
- Modify: `internal/server/site.go:322` — `siteLoginHandler` call to `signCookie`

- [ ] **Step 1: Update `siteHandler` (line 208)**

Change:
```go
if s.isValidAccessCookie(r, subdomainName, projectName, isCustomDomain) {
```
To:
```go
if s.isValidAccessCookie(r, subdomainName, projectName, isCustomDomain, rule.PasswordHash) {
```

- [ ] **Step 2: Update `siteLoginHandler` (line 322)**

Change:
```go
cookieValue := s.signCookie(cookieSubdomain, cookieProject, expiry)
```
To:
```go
cookieValue := s.signCookie(cookieSubdomain, cookieProject, expiry, rule.PasswordHash)
```

- [ ] **Step 3: Verify compilation passes**

Run: `cd /Users/chenzhong/Developer/droply && go build ./...`
Expected: BUILD SUCCESS

- [ ] **Step 4: Run existing tests to verify nothing is broken**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -v -run TestSiteHandler`
Expected: ALL PASS — existing tests should still pass because the test creates fresh cookies via login (which now sign with passwordHash), and validates with the same passwordHash.

- [ ] **Step 5: Commit**

```bash
git add internal/server/site.go
git commit -m "feat: include password_hash in cookie HMAC to invalidate on password change"
```

---

### Task 4: Write test — cookie invalidated after password change

**Files:**
- Modify: `internal/server/site_test.go` — add new test function

- [ ] **Step 1: Write the failing test `TestSiteHandlerCookieInvalidAfterPasswordChange`**

Add after the existing tests in `site_test.go`:

```go
func TestSiteHandlerCookieInvalidAfterPasswordChange(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	token, password := setupProtectedSite(t, srv, sitesDir)

	siteHandler := srv.NewSiteHandler()

	// Step 1: Login with original password to get a cookie.
	form := url.Values{}
	form.Set("password", password)
	form.Set("redirect", "/docs/hello.txt")
	form.Set("host", "alice.droplydoc.com")
	req := httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "alice.droplydoc.com"
	rr := httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("login: expected 302, got %d", rr.Code)
	}

	var oldCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			oldCookie = c
			break
		}
	}
	if oldCookie == nil {
		t.Fatal("expected _droply_access cookie after login")
	}

	// Step 2: Verify old cookie works.
	req = httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	req.AddCookie(oldCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Hello Droply") {
		t.Fatalf("old cookie should work before password change, got %d", rr.Code)
	}

	// Step 3: Change the password via API.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"password": "newpassword12345",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/alice/projects/docs/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update password: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Step 4: Old cookie should now be rejected.
	req = httptest.NewRequest(http.MethodGet, "/docs/hello.txt", nil)
	req.Host = "alice.droplydoc.com"
	req.AddCookie(oldCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("old cookie should be rejected after password change, got %d: %s", rr.Code, rr.Body.String())
	}

	// Step 5: Login with new password should work.
	form = url.Values{}
	form.Set("password", "newpassword12345")
	form.Set("redirect", "/docs/hello.txt")
	form.Set("host", "alice.droplydoc.com")
	req = httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "alice.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("login with new password: expected 302, got %d", rr.Code)
	}
}
```

- [ ] **Step 2: Run the test**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -v -run TestSiteHandlerCookieInvalidAfterPasswordChange`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/server/site_test.go
git commit -m "test: verify cookie invalidated after password change"
```

---

### Task 5: Write test — subdomain-scoped cookie with project-level rule edge case

**Files:**
- Modify: `internal/server/site_test.go` — add new test function

- [ ] **Step 1: Write the test `TestSiteHandlerSubdomainCookieWithProjectRule`**

This test verifies that a subdomain-scoped cookie remains valid when accessing a project that has its own project-level rule (as long as the subdomain password hasn't changed).

```go
func TestSiteHandlerSubdomainCookieWithProjectRule(t *testing.T) {
	srv, sitesDir := newTestSiteServer(t)
	token := registerAndGetToken(t, srv, "subtest@example.com", "password123")

	// Create subdomain.
	body, _ := json.Marshal(map[string]string{"name": "bob"})
	req := httptest.NewRequest(http.MethodPost, "/subdomains", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create subdomain: expected 201, got %d", rr.Code)
	}

	// Deploy project.
	deployProject(t, srv, token, "bob", "app", map[string]string{
		"index.html": "Hello App",
	})

	// Set SUBDOMAIN-level password rule.
	accessBody, _ := json.Marshal(map[string]interface{}{
		"password": "subdomainpass1",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set subdomain access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// ALSO set PROJECT-level password rule (different password).
	accessBody, _ = json.Marshal(map[string]interface{}{
		"password": "projectpass12",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/projects/app/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set project access: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	siteHandler := srv.NewSiteHandler()

	// Login with SUBDOMAIN password (at a project path — the login resolves to subdomain rule
	// because the project also has its own rule, but the form submits to the subdomain-level).
	// We need to login at a path that resolves to the subdomain-level rule.
	// Since FindAccessRuleForSite returns project-level first, we login at a non-existent project path
	// which will fall back to subdomain-level rule.
	// Actually, let's deploy a second project and login there to get a subdomain-scoped cookie.
	deployProject(t, srv, token, "bob", "other", map[string]string{
		"page.html": "Other Page",
	})

	form := url.Values{}
	form.Set("password", "subdomainpass1")
	form.Set("redirect", "/other/page.html")
	form.Set("host", "bob.droplydoc.com")
	req = httptest.NewRequest(http.MethodPost, "/_droply/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "bob.droplydoc.com"
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("subdomain login: expected 302, got %d: %s", rr.Code, rr.Body.String())
	}

	var subCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "_droply_access" {
			subCookie = c
			break
		}
	}
	if subCookie == nil {
		t.Fatal("expected subdomain-level cookie")
	}

	// Use subdomain-scoped cookie to access the project "app" (which has its own project-level rule).
	// Subdomain-scoped cookie should still grant access.
	req = httptest.NewRequest(http.MethodGet, "/app/index.html", nil)
	req.Host = "bob.droplydoc.com"
	req.AddCookie(subCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Hello App") {
		t.Fatalf("subdomain cookie should access project with project-level rule, got %d: %s", rr.Code, rr.Body.String())
	}

	// Now change the SUBDOMAIN password — subdomain-scoped cookie should be invalidated.
	accessBody, _ = json.Marshal(map[string]interface{}{
		"password": "newsubpass123",
	})
	req = httptest.NewRequest(http.MethodPut, "/subdomains/bob/access", bytes.NewReader(accessBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update subdomain password: expected 200, got %d", rr.Code)
	}

	// Old subdomain cookie should now be rejected.
	req = httptest.NewRequest(http.MethodGet, "/app/index.html", nil)
	req.Host = "bob.droplydoc.com"
	req.AddCookie(subCookie)
	rr = httptest.NewRecorder()
	siteHandler.ServeHTTP(rr, req)
	if !strings.Contains(rr.Body.String(), "_droply/login") {
		t.Fatalf("old subdomain cookie should be rejected after password change, got %d: %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run the test**

Run: `cd /Users/chenzhong/Developer/droply && go test ./internal/server/ -v -run TestSiteHandlerSubdomainCookieWithProjectRule`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/chenzhong/Developer/droply && go test ./...`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/site_test.go
git commit -m "test: verify subdomain-scoped cookie edge case with project-level rules"
```
