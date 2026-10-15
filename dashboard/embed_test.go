package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	h := HandlerFS(fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"assets/index-a1.js": {Data: []byte("js")},
	})
	tests := []struct {
		path   string
		status int
		body   string
		cache  string
	}{
		{"/", 200, "app", "no-cache"},
		{"/queues/emails", 200, "app", "no-cache"},
		{"/tasks/task_01", 200, "app", "no-cache"},
		{"/favicon.svg", 200, "<svg/>", ""},
		{"/assets/index-a1.js", 200, "js", "public, max-age=31536000, immutable"},
		{"/assets/index-old.js", 404, "", ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("GET %s = %d %q, want %d %q", tt.path, rec.Code, rec.Body.String(), tt.status, tt.body)
		}
		if got := rec.Header().Get("Cache-Control"); tt.status == 200 && got != tt.cache {
			t.Errorf("GET %s Cache-Control = %q, want %q", tt.path, got, tt.cache)
		}
	}
}

func TestPlaceholderWithoutBuild(t *testing.T) {
	h := HandlerFS(fstest.MapFS{".gitkeep": {}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Workly is running") {
		t.Fatalf("GET / = %d %s", rec.Code, rec.Body.String())
	}
}
