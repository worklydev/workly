package workspace

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestStaticResolve(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		header  string
		wantErr error
	}{
		{name: "no key configured, no header", apiKey: "", header: ""},
		{name: "no key configured, any header", apiKey: "", header: "Bearer whatever"},
		{name: "valid key", apiKey: "secret", header: "Bearer secret"},
		{name: "case-insensitive scheme", apiKey: "secret", header: "bearer secret"},
		{name: "missing header", apiKey: "secret", header: "", wantErr: ErrUnauthorized},
		{name: "wrong key", apiKey: "secret", header: "Bearer nope", wantErr: ErrUnauthorized},
		{name: "wrong scheme", apiKey: "secret", header: "Basic secret", wantErr: ErrUnauthorized},
		{name: "empty token", apiKey: "secret", header: "Bearer ", wantErr: ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/queues", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			id, err := Static{ID: Default, APIKey: tt.apiKey}.Resolve(r)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && id != Default {
				t.Fatalf("id = %q, want %q", id, Default)
			}
		})
	}
}
