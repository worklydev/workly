package signing

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestSign(t *testing.T) {
	// Fixed vector (computed independently with Python's hmac module), to be
	// shared with the SDK test suites.
	got := Sign([]byte("secret"), 1790000000, []byte(`{"user_id":"123"}`))
	want := "v1=00f7255c88a43b3e2d47b211ff8e71cf935601cabff5e7adad0de2cc72a4e3c6"
	if got != want {
		t.Fatalf("Sign = %q, want %q", got, want)
	}
}

func TestSignAll(t *testing.T) {
	body := []byte("hello")
	got := SignAll([][]byte{[]byte("new"), []byte("old")}, 1790000000, body)
	want := Sign([]byte("new"), 1790000000, body) + "," + Sign([]byte("old"), 1790000000, body)
	if got != want {
		t.Fatalf("SignAll = %q, want %q", got, want)
	}
	for _, secret := range []string{"new", "old"} {
		if err := Verify([]byte(secret), got, "1790000000", body, 0, time.Now()); err != nil {
			t.Errorf("Verify with %s secret: %v", secret, err)
		}
	}
}

func TestVerify(t *testing.T) {
	secret, body := []byte("secret"), []byte("hello")
	now := time.Unix(1790000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := Sign(secret, now.Unix(), body)

	tests := []struct {
		name      string
		secret    []byte
		signature string
		timestamp string
		body      string
		now       time.Time
		want      error
	}{
		{"valid", secret, sig, ts, "hello", now, nil},
		{"rotation list", secret, "v1=00ff, " + sig, ts, "hello", now, nil},
		{"wrong secret", []byte("other"), sig, ts, "hello", now, ErrInvalidSignature},
		{"tampered body", secret, sig, ts, "hellO", now, ErrInvalidSignature},
		{"tampered timestamp", secret, sig, "1790000001", "hello", now, ErrInvalidSignature},
		{"unknown scheme", secret, "v0=" + sig[3:], ts, "hello", now, ErrInvalidSignature},
		{"too old", secret, sig, ts, "hello", now.Add(6 * time.Minute), ErrExpired},
		{"missing signature", secret, "", ts, "hello", now, ErrMalformed},
		{"bad timestamp", secret, sig, "yesterday", "hello", now, ErrMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(tt.secret, tt.signature, tt.timestamp, []byte(tt.body), 5*time.Minute, tt.now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Verify = %v, want %v", err, tt.want)
			}
		})
	}
}
