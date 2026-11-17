// Package workly holds the docs, embedded, so the server can hand them to
// agents (internal/docs). Go can only embed files at or below a package's
// directory, so this is at the root of the module.
package workly

import "embed"

// Docs is docs/*.md and the SDKs' READMEs, as in the repo.
//
//go:embed docs/*.md sdks/python/README.md sdks/typescript/README.md
var Docs embed.FS
