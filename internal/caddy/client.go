package caddy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
)

// Client interacts with the Caddy admin API to manage dynamic routes.
type Client struct {
	adminURL   string
	baseDomain string
	sitesDir   string
	httpClient *http.Client
}

// NewClient creates a new Caddy admin API client.
func NewClient(adminURL, baseDomain, sitesDir string) *Client {
	return &Client{
		adminURL:   adminURL,
		baseDomain: baseDomain,
		sitesDir:   sitesDir,
		httpClient: &http.Client{},
	}
}

// caddyRoute represents a Caddy HTTP route configuration.
type caddyRoute struct {
	ID       string          `json:"@id"`
	Match    []caddyMatch    `json:"match"`
	Handle   []caddyHandler  `json:"handle"`
	Terminal bool            `json:"terminal"`
}

type caddyMatch struct {
	Host []string `json:"host"`
}

type caddyHandler struct {
	Handler   string          `json:"handler"`
	Root      string          `json:"root,omitempty"`
	Upstreams []caddyUpstream `json:"upstreams,omitempty"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

// buildCustomDomainRoute constructs a Caddy route for a custom domain.
func (c *Client) buildCustomDomainRoute(domain, subdomainName, projectName string) caddyRoute {
	root := filepath.Join(c.sitesDir, subdomainName, projectName)
	return caddyRoute{
		ID:    fmt.Sprintf("domain-%s", domain),
		Match: []caddyMatch{{Host: []string{domain}}},
		Handle: []caddyHandler{
			{Handler: "file_server", Root: root},
		},
		Terminal: true,
	}
}

// AddCustomDomainRoute adds a custom domain route to the running Caddy config.
func (c *Client) AddCustomDomainRoute(domain, subdomainName, projectName string) error {
	route := c.buildCustomDomainRoute(domain, subdomainName, projectName)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// RemoveCustomDomainRoute removes a custom domain route from the running Caddy config.
func (c *Client) RemoveCustomDomainRoute(domain string) error {
	return c.delete(fmt.Sprintf("/id/domain-%s", domain))
}

// SetCustomDomainProtected switches a custom domain route from unprotected (file_server) to protected (reverse_proxy).
func (c *Client) SetCustomDomainProtected(domain string, proxyAddr string) error {
	_ = c.delete(fmt.Sprintf("/id/domain-%s", domain))
	route := caddyRoute{
		ID:    fmt.Sprintf("domain-%s", domain),
		Match: []caddyMatch{{Host: []string{domain}}},
		Handle: []caddyHandler{
			{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: proxyAddr}}},
		},
		Terminal: true,
	}
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// SetCustomDomainUnprotected switches a custom domain route from protected (reverse_proxy) to unprotected (file_server).
func (c *Client) SetCustomDomainUnprotected(domain, subdomainName, projectName string) error {
	_ = c.delete(fmt.Sprintf("/id/domain-%s", domain))
	route := c.buildCustomDomainRoute(domain, subdomainName, projectName)
	return c.postJSON("/config/apps/http/servers/main/routes", route)
}

// postJSON sends a POST request with a JSON body to the Caddy admin API.
func (c *Client) postJSON(path string, data interface{}) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal caddy request: %w", err)
	}

	resp, err := c.httpClient.Post(c.adminURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("caddy POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("caddy POST %s: unexpected status %d", path, resp.StatusCode)
	}
	return nil
}

// delete sends a DELETE request to the Caddy admin API.
func (c *Client) delete(path string) error {
	req, err := http.NewRequest(http.MethodDelete, c.adminURL+path, nil)
	if err != nil {
		return fmt.Errorf("build DELETE request %s: %w", path, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("caddy DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("caddy DELETE %s: unexpected status %d", path, resp.StatusCode)
	}
	return nil
}
