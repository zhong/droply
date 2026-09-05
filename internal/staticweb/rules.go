package staticweb

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

const maxRuleBytes = 64 << 10
const maxRules = 100
const maxLineBytes = 2048

type pattern struct {
	path string
	wild bool
}

func (p pattern) match(value string) (string, bool) {
	if p.wild {
		return strings.TrimPrefix(value, p.path), strings.HasPrefix(value, p.path)
	}
	return "", value == p.path
}

type headerRule struct {
	source  pattern
	headers http.Header
}
type redirectRule struct {
	source pattern
	target *url.URL
	status int
}

// Load validates immutable deployment configuration. Its returned Site may be
// shared by concurrent requests; callers own artifact retention while serving.
func Load(dir string) (*Site, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("site root is not a plain directory")
	}
	s := &Site{dir: dir, mode: "static"}
	config, err := readConfig(dir, "_droply.toml")
	if err != nil {
		return nil, err
	}
	if config != nil {
		cfg := struct {
			Site struct {
				Mode string `toml:"mode"`
			} `toml:"site"`
		}{}
		cfg.Site.Mode = "static"
		metadata, err := toml.Decode(string(config), &cfg)
		if err != nil {
			return nil, fmt.Errorf("_droply.toml: %w", err)
		}
		if len(metadata.Undecoded()) != 0 {
			return nil, errors.New("_droply.toml contains unsupported keys")
		}
		if cfg.Site.Mode != "static" && cfg.Site.Mode != "spa" {
			return nil, errors.New("site.mode must be static or spa")
		}
		s.mode = cfg.Site.Mode
	}
	headers, err := readConfig(dir, "_headers")
	if err != nil {
		return nil, err
	}
	s.headers, err = parseHeaders(headers)
	if err != nil {
		return nil, err
	}
	redirects, err := readConfig(dir, "_redirects")
	if err != nil {
		return nil, err
	}
	s.redirects, err = parseRedirects(redirects)
	if err != nil {
		return nil, err
	}
	if err = s.validateLoops(); err != nil {
		return nil, err
	}
	return s, nil
}
func Validate(dir string) error { _, err := Load(dir); return err }
func readConfig(dir, name string) ([]byte, error) {
	filename := filepath.Join(dir, name)
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxRuleBytes {
		return nil, fmt.Errorf("%s must be a regular file no larger than 64 KiB", name)
	}
	return os.ReadFile(filename)
}
func validPath(value string) bool {
	if value == "" || len(value) > 4096 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\%?#") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	canonical := strings.TrimSuffix(value, "/")
	if canonical == "" {
		return true
	}
	if path.Clean(canonical) != canonical {
		return false
	}
	for i, part := range strings.Split(strings.TrimPrefix(canonical, "/"), "/") {
		if strings.HasPrefix(part, ".") && !(i == 0 && part == ".well-known") {
			return false
		}
		switch strings.ToLower(part) {
		case "_headers", "_redirects", "_droply.toml", "_droply", "_auth", "_internal", "manifest.json":
			return false
		}
	}
	return value != "/.well-known/acme-challenge" && !strings.HasPrefix(value, "/.well-known/acme-challenge/")
}
func parsePattern(value string) (pattern, error) {
	p := pattern{path: value}
	if prefix, ok := strings.CutSuffix(value, "*"); ok {
		p.wild = true
		p.path = prefix
	}
	if strings.Contains(p.path, "*") || strings.Contains(p.path, ":") || !validPath(p.path) {
		return pattern{}, errors.New("patterns must be safe exact paths or have one terminal *")
	}
	return p, nil
}
func ruleLines(data []byte, visit func(int, string) error) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), maxLineBytes+1)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if len(text) > maxLineBytes {
			return fmt.Errorf("rule line %d exceeds 2048 bytes", line)
		}
		if strings.TrimSpace(text) == "" || strings.HasPrefix(strings.TrimSpace(text), "#") {
			continue
		}
		if err := visit(line, text); err != nil {
			return fmt.Errorf("rule line %d: %w", line, err)
		}
	}
	return scanner.Err()
}
func parseHeaders(data []byte) ([]headerRule, error) {
	var rules []headerRule
	seen := map[string]bool{}
	headerCount := 0
	err := ruleLines(data, func(_ int, line string) error {
		if line[0] != ' ' && line[0] != '\t' {
			if len(rules) > 0 && len(rules[len(rules)-1].headers) == 0 {
				return errors.New("empty _headers block")
			}
			p, err := parsePattern(strings.TrimSpace(line))
			if err != nil {
				return err
			}
			if seen[line] || len(rules) >= maxRules {
				return errors.New("duplicate or excessive header blocks")
			}
			seen[line] = true
			rules = append(rules, headerRule{source: p, headers: http.Header{}})
			return nil
		}
		if len(rules) == 0 {
			return errors.New("header requires an unindented path block")
		}
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || !validHeaderName(name) || value == "" {
			return errors.New("header must be Name: value")
		}
		for _, c := range value {
			if unicode.IsControl(c) {
				return errors.New("control characters are not allowed in header values")
			}
		}
		name = http.CanonicalHeaderKey(name)
		if protectedHeader(name) {
			return fmt.Errorf("header %s is controlled by Droply", name)
		}
		current := rules[len(rules)-1].headers
		if _, exists := current[name]; exists {
			return errors.New("duplicate header in block")
		}
		headerCount++
		if headerCount > maxRules*10 {
			return errors.New("too many headers")
		}
		current.Set(name, value)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("_headers: %w", err)
	}
	if len(rules) > 0 && len(rules[len(rules)-1].headers) == 0 {
		return nil, errors.New("_headers: empty block")
	}
	return rules, nil
}
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}
func protectedHeader(name string) bool {
	switch name {
	case "Authorization", "Proxy-Authorization", "Www-Authenticate", "Proxy-Authenticate", "Authentication-Info", "Cookie", "Set-Cookie", "Set-Cookie2", "Location", "Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Te", "Trailer", "Upgrade", "Content-Length", "Content-Range", "Content-Encoding", "Accept-Ranges", "Etag", "Last-Modified", "Host":
		return true
	}
	return false
}
func parseRedirects(data []byte) ([]redirectRule, error) {
	var rules []redirectRule
	seen := map[string]bool{}
	err := ruleLines(data, func(_ int, line string) error {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return errors.New("redirect requires source target status; conditions and forced rules are unsupported")
		}
		source, err := parsePattern(fields[0])
		if err != nil {
			return err
		}
		if seen[fields[0]] || len(rules) >= maxRules {
			return errors.New("duplicate or excessive redirect rules")
		}
		seen[fields[0]] = true
		code, err := strconv.Atoi(fields[2])
		if err != nil {
			return errors.New("invalid redirect status")
		}
		switch code {
		case 200, 301, 302, 303, 307, 308:
		default:
			return errors.New("unsupported redirect status")
		}
		target, err := url.Parse(fields[1])
		if err != nil || target.User != nil || target.Opaque != "" {
			return errors.New("invalid redirect target")
		}
		if strings.Contains(fields[1], ":splat") && !source.wild {
			return errors.New(":splat requires a wildcard source")
		}
		// Substitution is path-only; using captured paths in an authority/query would
		// change their interpretation and is outside this compatibility subset.
		if strings.Contains(target.Host, ":splat") || strings.Contains(target.RawQuery, ":splat") || strings.Contains(target.Fragment, ":splat") {
			return errors.New(":splat is only supported in the target path")
		}
		if strings.Count(target.Path, ":splat") > 1 || strings.Contains(strings.ReplaceAll(target.Path, ":splat", ""), ":") {
			return errors.New("unsupported target placeholder")
		}
		local := target.Scheme == "" && target.Host == ""
		if !local {
			if code == 200 {
				return errors.New("external proxy rewrites are unsupported")
			}
			if target.Path == "" {
				target.Path = "/"
			}
			if (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
				return errors.New("external redirects require http or https")
			}
		}
		if !validPath(strings.ReplaceAll(target.Path, ":splat", "capture")) {
			return errors.New("redirect target uses a reserved or unsafe path")
		}
		rules = append(rules, redirectRule{source: source, target: target, status: code})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("_redirects: %w", err)
	}
	return rules, nil
}
func overlap(a, b pattern) bool {
	if a.wild && b.wild {
		return strings.HasPrefix(a.path, b.path) || strings.HasPrefix(b.path, a.path)
	}
	if a.wild {
		_, ok := a.match(b.path)
		return ok
	}
	if b.wild {
		_, ok := b.match(a.path)
		return ok
	}
	return a.path == b.path
}
func (s *Site) validateLoops() error {
	graph := make([][]int, len(s.redirects))
	for i, r := range s.redirects {
		if r.target.Host != "" {
			continue
		}
		target := pattern{path: r.target.Path}
		if prefix, _, ok := strings.Cut(target.path, ":splat"); ok {
			target.path = prefix
			target.wild = true
		}
		// Rewrites to existing files terminate without applying another rule. This
		// permits the common /* /index.html 200 rule while rejecting unresolved cycles.
		if r.status == 200 && !target.wild && s.hasFile(target.path) {
			continue
		}
		targets := []pattern{target}
		if !target.wild && r.status != 200 {
			file, _, slash := s.resolve(target.path)
			if file != nil {
				file.Close()
				if slash {
					targets = append(targets, pattern{path: target.path + "/"})
				}
			}
		}
		for j, next := range s.redirects {
			for _, candidate := range targets {
				if overlap(candidate, next.source) {
					graph[i] = append(graph[i], j)
					break
				}
			}
		}
	}
	states := make([]int, len(graph))
	var visit func(int) bool
	visit = func(i int) bool {
		if states[i] == 1 {
			return false
		}
		if states[i] == 2 {
			return true
		}
		states[i] = 1
		for _, j := range graph[i] {
			if !visit(j) {
				return false
			}
		}
		states[i] = 2
		return true
	}
	for i := range graph {
		if !visit(i) {
			return errors.New("_redirects contains a possible local rule loop")
		}
	}
	return nil
}
