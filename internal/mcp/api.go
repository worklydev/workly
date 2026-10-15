package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// apiClient calls the /v1 handler in-process. Requests carry the /mcp
// request's context, and with it the workspace resolved from its credentials.
type apiClient struct {
	h http.Handler
}

// apiError is an error response from the API, in its stable shape.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func (c apiClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var r io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RequestURI = target

	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	c.h.ServeHTTP(rec, req)
	if rec.status >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(rec.body.Bytes(), &e) != nil || e.Error.Code == "" {
			return &apiError{Status: rec.status, Code: "unknown", Message: rec.body.String()}
		}
		return &apiError{Status: rec.status, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out == nil || rec.body.Len() == 0 {
		return nil
	}
	if err := json.Unmarshal(rec.body.Bytes(), out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// recorder is a minimal in-memory http.ResponseWriter.
type recorder struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.body.Write(b)
}

func apiPath(format string, segments ...string) string {
	args := make([]any, len(segments))
	for i, s := range segments {
		args[i] = url.PathEscape(s)
	}
	return fmt.Sprintf(format, args...)
}
