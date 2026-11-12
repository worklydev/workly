package api

import "github.com/worklydev/workly/internal/store"

// validateCallback checks an on_complete. Callbacks always go to
// CallbackQueue, which Workly creates when it needs it, so there is no
// queue to check.
func validateCallback(cb *Callback, maxPayloadBytes int64) (*store.Callback, error) {
	if cb == nil {
		return nil, nil
	}
	r, err := validateRequest(request{url: cb.Url, headers: cb.Headers}, maxPayloadBytes)
	if err != nil {
		return nil, inField("on_complete", err)
	}
	return &store.Callback{URL: r.url, Headers: r.headers}, nil
}

func toCallback(cb *store.Callback) *Callback {
	if cb == nil {
		return nil
	}
	out := &Callback{Url: cb.URL}
	if len(cb.Headers) > 0 {
		out.Headers = &cb.Headers
	}
	return out
}
