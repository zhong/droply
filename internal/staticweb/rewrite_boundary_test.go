package staticweb_test

import (
	"net/http"
	"testing"
)

// A 200 rule stops rule matching, but does not always stop HTTP redirects.
// Keep this counterexample before considering removal of all rewrite graph edges.
func TestSelfRewriteStillCanonicalizesDirectory(t *testing.T) {
	site := fixture(t, map[string]string{
		"folder/index.html": "folder",
		"_redirects":        "/folder /folder 200",
	})
	response := request(site, "/folder")
	if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != "/folder/" {
		t.Fatalf("directory self rewrite: status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	response = request(site, "/folder/")
	if response.Code != http.StatusOK || response.Body.String() != "folder" {
		t.Fatalf("canonical directory: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRewriteBoundaryWithoutRecursiveMatching(t *testing.T) {
	site := fixture(t, map[string]string{
		"assets/file.txt":          "file",
		"assets/folder/index.html": "folder",
		"_redirects":               "/alias/* /assets/:splat 200\n/missing /still-missing 200\n/still-missing /assets/file.txt 302",
	})
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{path: "/alias/file.txt", status: http.StatusOK, body: "file"},
		{path: "/alias/folder", status: http.StatusOK, body: "folder"},
		{path: "/alias/missing", status: http.StatusNotFound},
		{path: "/missing", status: http.StatusNotFound},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := request(site, test.path)
			if response.Code != test.status || response.Header().Get("Location") != "" {
				t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
			}
			if test.body != "" && response.Body.String() != test.body {
				t.Fatalf("body=%q", response.Body.String())
			}
		})
	}
}
