// Package signing signs and verifies deliveries. The scheme matches the
// SDKs' verify() helpers, so changes here are breaking changes:
//
//	Workly-Timestamp: <unix seconds>
//	Workly-Signature: v1=<hex HMAC-SHA256 of "<timestamp>.<body>">
//
// The version prefix allows adding schemes (or several signatures during key
// rotation, comma-separated) without breaking verifiers.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderSignature = "Workly-Signature"
	HeaderTimestamp = "Workly-Timestamp"
)

var (
	ErrInvalidSignature = errors.New("signature does not match")
	ErrExpired          = errors.New("timestamp outside tolerance")
	ErrMalformed        = errors.New("missing or malformed signature headers")
)

// Sign returns the Workly-Signature header value.
func Sign(secret []byte, timestamp int64, body []byte) string {
	return "v1=" + hex.EncodeToString(mac(secret, timestamp, body))
}

// SignAll returns a Workly-Signature header value with one signature per
// secret, for rotating the signing secret: endpoints verifying with any of
// them accept the delivery.
func SignAll(secrets [][]byte, timestamp int64, body []byte) string {
	sigs := make([]string, len(secrets))
	for i, s := range secrets {
		sigs[i] = Sign(s, timestamp, body)
	}
	return strings.Join(sigs, ",")
}

func mac(secret []byte, timestamp int64, body []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(strconv.FormatInt(timestamp, 10)))
	h.Write([]byte("."))
	h.Write(body)
	return h.Sum(nil)
}

// Verify checks the signature and timestamp headers against body. A
// tolerance of zero skips the timestamp age check.
func Verify(secret []byte, signature, timestamp string, body []byte, tolerance time.Duration, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || signature == "" {
		return ErrMalformed
	}
	if tolerance > 0 && now.Sub(time.Unix(ts, 0)).Abs() > tolerance {
		return ErrExpired
	}
	want := mac(secret, ts, body)
	for _, part := range strings.Split(signature, ",") {
		hexSig, ok := strings.CutPrefix(strings.TrimSpace(part), "v1=")
		if !ok {
			continue
		}
		if got, err := hex.DecodeString(hexSig); err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrInvalidSignature
}
