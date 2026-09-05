package server

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/zhong/droply/internal/model"
)

//go:embed visitor_assets/*
var visitorAssets embed.FS

var loginPageTemplate = template.Must(template.ParseFS(visitorAssets, "visitor_assets/index.html"))

// serveVisitorAsset exposes only the login UI bundle, before visitor authentication.
// The template and deployment files must never be served through this route.
func (s *Server) serveVisitorAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.resolveHost(r.Context(), r.Host); !ok {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/_droply/ui/")
	switch name {
	case "visitor.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case "visitor.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	default:
		http.NotFound(w, r)
		return
	}
	data, err := visitorAssets.ReadFile("visitor_assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// renderLoginPage keeps the native form usable even if JavaScript cannot load.
func (s *Server) renderLoginPage(w http.ResponseWriter, r *http.Request, rule *model.AccessRule, errorMsg string) {
	if errorMsg == "Incorrect password" {
		errorMsg = "密码不正确，请重试。"
	}
	data := map[string]any{
		"Error":        errorMsg,
		"Redirect":     r.URL.RequestURI(),
		"Host":         r.Host,
		"ShowPassword": rule.HasPassword,
		"ShowWeWork":   rule.WeWorkEnabled && s.wework != nil,
	}
	var body bytes.Buffer
	if err := loginPageTemplate.Execute(&body, data); err != nil {
		http.Error(w, "cannot render login page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; "+
		"connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(body.Bytes())
}
