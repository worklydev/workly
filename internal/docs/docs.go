// Package docs serves the docs embedded in the binary, by page, as the docs
// site names them (docs-site/scripts/sync.mjs): "index" for docs/README.md,
// "sdks/python" for sdks/python/README.md, otherwise the file's name.
package docs

import (
	"fmt"
	"io/fs"
	pathpkg "path"
	"regexp"
	"slices"
	"strings"

	"github.com/worklydev/workly"
)

// Site is where the docs are published; links between pages point there.
const Site = "https://workly.run/docs/"

// Page is one page of the docs.
type Page struct {
	Name  string `json:"page"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// files maps a page to its file in workly.Docs, in the docs site's order.
var files = func() map[string]string {
	m := map[string]string{"sdks/python": "sdks/python/README.md", "sdks/typescript": "sdks/typescript/README.md"}
	entries, err := fs.ReadDir(workly.Docs, "docs")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "README" {
			name = "index"
		}
		m[name] = "docs/" + e.Name()
	}
	return m
}()

// order is the docs site's sidebar; pages not in it come after, by name.
var order = []string{"index", "quickstart", "concepts", "schedules", "groups", "workflows", "callbacks", "alerts", "deploy", "self-hosting", "mcp", "sdks/python", "sdks/typescript", "cli", "configuration", "metrics", "performance"}

// Pages lists every page, in the docs site's order.
func Pages() []Page {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	rank := func(n string) int {
		if i := slices.Index(order, n); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortFunc(names, func(a, b string) int {
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	pages := make([]Page, len(names))
	for i, name := range names {
		text, _ := Get(name)
		title, _, _ := strings.Cut(strings.TrimPrefix(text, "# "), "\n")
		pages[i] = Page{Name: name, Title: title, URL: url(name)}
	}
	return pages
}

// Get returns a page's Markdown, with links to other pages pointing at the
// docs site.
func Get(name string) (string, error) {
	file, ok := files[strings.Trim(name, "/")]
	if !ok {
		return "", fmt.Errorf("no docs page %q", name)
	}
	b, err := fs.ReadFile(workly.Docs, file)
	if err != nil {
		return "", err
	}
	return link.ReplaceAllStringFunc(string(b), func(m string) string {
		target := link.FindStringSubmatch(m)[1]
		return "](" + resolve(file, target) + ")"
	}), nil
}

var link = regexp.MustCompile(`\]\(([^)\s:]+)\)`)

// resolve turns a link relative to file into an absolute one.
func resolve(file, target string) string {
	if strings.HasPrefix(target, "#") {
		return target
	}
	path, anchor, _ := strings.Cut(target, "#")
	if anchor != "" {
		anchor = "#" + anchor
	}
	dir := file[:strings.LastIndex(file, "/")+1]
	full := pathpkg.Clean(dir + path)
	for name, f := range files {
		if f == full {
			return url(name) + anchor
		}
	}
	if full == "api/openapi.yaml" {
		return Site + "api/openapi.yaml" + anchor
	}
	return "https://github.com/worklydev/workly/blob/main/" + full + anchor
}

func url(name string) string {
	if name == "index" {
		return Site
	}
	return Site + name + "/"
}
