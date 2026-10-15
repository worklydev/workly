// Package workspace resolves which workspace a request belongs to.
//
// Every piece of data in Workly is scoped to a workspace, but the workspace
// never appears in API paths: it is derived from the request's credentials.
// That keeps client code identical across `workly dev`, self-hosted and
// hosted deployments.
package workspace

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

// ID identifies a workspace.
type ID string

// Default is the single workspace used by `workly dev` and self-hosted servers.
const Default ID = "default"

// ErrUnauthorized is returned by a Resolver when the request carries missing
// or invalid credentials.
var ErrUnauthorized = errors.New("unauthorized")

// Resolver maps an incoming request to a workspace.
type Resolver interface {
	Resolve(r *http.Request) (ID, error)
}

// Static resolves every request to one workspace. If APIKey is non-empty,
// requests must present it as a bearer token.
type Static struct {
	ID     ID
	APIKey string
}

func (s Static) Resolve(r *http.Request) (ID, error) {
	if s.APIKey != "" {
		token, ok := BearerToken(r)
		if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.APIKey)) != 1 {
			return "", ErrUnauthorized
		}
	}
	return s.ID, nil
}

// BearerToken extracts the token from an `Authorization: Bearer <token>` header.
func BearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

type ctxKey struct{}

// WithID returns a context carrying the resolved workspace.
func WithID(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the workspace resolved for the current request.
func FromContext(ctx context.Context) (ID, bool) {
	id, ok := ctx.Value(ctxKey{}).(ID)
	return id, ok
}
