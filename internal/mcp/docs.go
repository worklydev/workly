package mcp

import (
	"context"

	"github.com/worklydev/workly/internal/docs"
)

// markdown is a tool result sent as it is, rather than encoded as JSON.
type markdown string

var getDocs = newTool("get_docs", "Read the docs",
	"Workly's documentation, for this server's version, as Markdown. Without page, lists the pages with their titles; with page, returns that page. Read a page before writing code or configuration against a feature: quickstart and concepts for how delivery, retries and signing work; schedules, groups, workflows, callbacks and alerts for those features; deploy for workly.yaml; sdks/python and sdks/typescript for every SDK method; cli for every command and flag; configuration for the server's settings. Links to https://workly.run/docs/<page>/ in a page are other pages this tool returns.",
	object(map[string]any{
		"page": prop("string", "The page to read; omit to list them.", "enum", pageNames()),
	}), readOnly,
	func(_ context.Context, _ apiClient, args struct {
		Page string `json:"page"`
	}) (any, error) {
		if args.Page == "" {
			return map[string]any{"pages": docs.Pages()}, nil
		}
		text, err := docs.Get(args.Page)
		if err != nil {
			return nil, argumentError{err}
		}
		return markdown(text), nil
	})

func pageNames() []string {
	var names []string
	for _, p := range docs.Pages() {
		names = append(names, p.Name)
	}
	return names
}
