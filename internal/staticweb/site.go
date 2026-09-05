// Package staticweb serves versioned static sites without directory browsing.
package staticweb

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Path     string
	Prefix   string
	Private  bool
	Preview  bool
	ETagSeed string
}
type Site struct {
	dir, mode string
	headers   []headerRule
	redirects []redirectRule
}

var fingerprint = regexp.MustCompile(`(?:^|[.-])[0-9a-fA-F]{8,}(?:[.-]|$)`)

func (s *Site) file(name string) (*os.File, error) {
	// Refuse every symlink component even if configuration validation is being run
	// against a legacy directory rather than an already verified artifact.
	current := s.dir
	for part := range strings.SplitSeq(strings.TrimPrefix(name, "/"), "/") {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, os.ErrPermission
		}
	}
	f, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, os.ErrNotExist
	}
	return f, nil
}
func (s *Site) hasFile(name string) bool {
	f, _, _ := s.resolve(name)
	if f == nil {
		return false
	}
	f.Close()
	return true
}

// resolve returns the selected regular file and whether its directory needs '/'.
func (s *Site) resolve(name string) (*os.File, string, bool) {
	if !validPath(name) {
		return nil, "", false
	}
	if f, err := s.file(name); err == nil {
		return f, name, false
	}
	directory := filepath.Join(s.dir, filepath.FromSlash(strings.TrimPrefix(name, "/")))
	if info, err := os.Lstat(directory); err == nil && info.IsDir() {
		index := strings.TrimSuffix(name, "/") + "/index.html"
		if f, err := s.file(index); err == nil {
			return f, index, !strings.HasSuffix(name, "/")
		}
		return nil, "", false
	}
	if path.Ext(name) == "" && !strings.HasSuffix(name, "/") {
		if f, err := s.file(name + ".html"); err == nil {
			return f, name + ".html", false
		}
	}
	return nil, "", false
}

// ServeHTTP must be called only after the platform has authorized the original
// request. Rewrites stay inside this Site and never invoke platform routes.
func (s *Site) ServeHTTP(w http.ResponseWriter, r *http.Request, opts Options) {
	guard := &responseGuard{ResponseWriter: w, options: opts}
	original := opts.Path
	if original == "" {
		original = r.URL.Path
	}
	if !validPath(original) || (opts.Prefix != "" && (!validPath(opts.Prefix) || strings.HasSuffix(opts.Prefix, "/"))) {
		http.NotFound(guard, r)
		return
	}
	for _, rule := range s.headers {
		if _, ok := rule.source.match(original); ok {
			for name, values := range rule.headers {
				w.Header()[name] = append([]string(nil), values...)
			}
		}
	}
	selected := original
	for _, rule := range s.redirects {
		captured, ok := rule.source.match(original)
		if !ok {
			continue
		}
		target := *rule.target
		target.Path = strings.ReplaceAll(target.Path, ":splat", captured)
		target.RawPath = ""
		if !validPath(target.Path) {
			http.Error(guard, "invalid rewrite target", http.StatusBadRequest)
			return
		}
		if target.RawQuery == "" && !target.ForceQuery {
			target.RawQuery = r.URL.RawQuery
		}
		if rule.status != 200 {
			if target.Host == "" {
				target.Path = opts.Prefix + target.Path
			} else if strings.EqualFold(target.Host, r.Host) && target.Path == r.URL.Path && target.RawQuery == r.URL.RawQuery {
				http.Error(guard, "redirect loop", http.StatusLoopDetected)
				return
			}
			// Also protect against any direct local self-loop that runtime substitutions
			// reveal but conservative deployment validation could not classify.
			if target.Host == "" && target.Path == r.URL.Path && target.RawQuery == r.URL.RawQuery {
				http.Error(guard, "redirect loop", http.StatusLoopDetected)
				return
			}
			http.Redirect(guard, r, target.String(), rule.status)
			return
		}
		selected = target.Path
		// 200 is a terminal local rewrite. It never recursively applies redirect rules.
		r = r.Clone(r.Context())
		r.URL.RawQuery = target.RawQuery
		break
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(guard, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f, filename, trailing := s.resolve(selected)
	if f != nil && trailing && selected == original {
		f.Close()
		target := &url.URL{Path: opts.Prefix + strings.TrimSuffix(selected, "/") + "/", RawQuery: r.URL.RawQuery}
		http.Redirect(guard, r, target.String(), http.StatusMovedPermanently)
		return
	}
	// Missing asset requests must retain their 404 semantics even with an SPA
	// catch-all rewrite; an HTML entry point is not a JavaScript/image response.
	if f != nil && selected != original && path.Ext(original) != "" && !htmlPath(original) && htmlPath(filename) && !s.hasFile(original) {
		f.Close()
		f = nil
	}
	if f == nil && selected == original && s.mode == "spa" && path.Ext(strings.TrimSuffix(original, "/")) == "" && !strings.HasPrefix(original, "/.well-known/") && acceptsHTML(r.Header.Get("Accept")) {
		f, _ = s.file("/index.html")
		filename = "/index.html"
	}
	if f == nil {
		s.notFound(guard, r, opts, original)
		return
	}
	defer f.Close()
	s.serveFile(guard, r, f, filename, original, opts)
}
func htmlPath(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".htm"
}
func acceptsHTML(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	bestSpecificity := -1
	bestQuality := 0.0
	for item := range strings.SplitSeq(value, ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil {
			continue
		}
		specificity := 0
		switch media {
		case "text/html":
			specificity = 3
		case "text/*":
			specificity = 2
		case "*/*":
			specificity = 1
		default:
			continue
		}
		quality := 1.0
		if text, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(text, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			bestQuality = quality
		} else if specificity == bestSpecificity {
			bestQuality = max(bestQuality, quality)
		}
	}
	return bestQuality > 0
}

func (s *Site) serveFile(w *responseGuard, r *http.Request, f *os.File, filename, original string, opts Options) {
	seed := opts.ETagSeed
	if seed == "" {
		seed = s.dir
	}
	sum := sha256.Sum256([]byte(seed + "\x00" + original + "\x00" + filename))
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.html = htmlPath(filename)
	if w.Header().Get("Cache-Control") == "" {
		policy := "public, max-age=0, must-revalidate"
		if fingerprint.MatchString(path.Base(filename)) {
			policy = "public, max-age=31536000, immutable"
		}
		w.Header().Set("Cache-Control", policy)
	}
	if w.Header().Get("X-Content-Type-Options") == "" {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	// Immutable version ETags, rather than second-precision modification times,
	// drive revalidation. ServeContent supplies byte ranges and HEAD semantics.
	http.ServeContent(w, r, path.Base(filename), time.Time{}, f)
}
func (s *Site) notFound(w *responseGuard, r *http.Request, opts Options, original string) {
	f, err := s.file("/404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	r = r.Clone(r.Context())
	for _, name := range []string{"Range", "If-Range", "If-None-Match", "If-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		r.Header.Del(name)
	}
	w.status = http.StatusNotFound
	s.serveFile(w, r, f, "/404.html", original, opts)
}

type responseGuard struct {
	http.ResponseWriter
	options Options
	html    bool
	status  int
	written bool
}

func (w *responseGuard) WriteHeader(status int) {
	if w.written {
		return
	}
	w.written = true
	if w.status != 0 && status == http.StatusOK {
		status = w.status
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	if w.html || strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/html") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if w.options.Private {
		w.Header().Set("Cache-Control", "private, no-store")
		addVary(w.Header(), "Cookie")
	}
	if w.options.Preview {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseGuard) Write(p []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func addVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for item := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

var _ io.Writer = (*responseGuard)(nil)
