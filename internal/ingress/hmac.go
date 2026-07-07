package ingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrMissingHMACSecret     = errors.New("hmac secret required")
	ErrMissingHMACSignature  = errors.New("hmac signature required")
	ErrInvalidHMACSignature  = errors.New("hmac signature invalid")
	ErrMissingHMACTimestamp  = errors.New("hmac timestamp required")
	ErrInvalidHMACTimestamp  = errors.New("hmac timestamp invalid")
	ErrStaleHMACTimestamp    = errors.New("hmac timestamp outside allowed skew")
	ErrUnsignedHMACTimestamp = errors.New("hmac timestamp must be signed")
)

type HMACVerification struct {
	Secret           []byte
	Body             []byte
	Signature        string
	Signatures       []string
	Timestamp        string
	Now              time.Time
	MaxSkew          time.Duration
	IncludeTimestamp bool
}

func SignRawBodyHMACSHA256(secret, body []byte, timestamp string, includeTimestamp bool) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(hmacPayload(body, timestamp, includeTimestamp))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifyRawBodyHMACSHA256(v HMACVerification) error {
	if len(v.Secret) == 0 {
		return ErrMissingHMACSecret
	}
	signatures := normalizedSignatures(v)
	if len(signatures) == 0 {
		return ErrMissingHMACSignature
	}
	if v.MaxSkew > 0 {
		if !v.IncludeTimestamp {
			return ErrUnsignedHMACTimestamp
		}
		if strings.TrimSpace(v.Timestamp) == "" {
			return ErrMissingHMACTimestamp
		}
		ts, err := parseHMACTimestamp(v.Timestamp)
		if err != nil {
			return err
		}
		now := v.Now
		if now.IsZero() {
			now = time.Now().UTC()
		}
		if durationAbs(now.Sub(ts)) > v.MaxSkew {
			return ErrStaleHMACTimestamp
		}
	}
	expectedMAC := hmac.New(sha256.New, v.Secret)
	_, _ = expectedMAC.Write(hmacPayload(v.Body, v.Timestamp, v.IncludeTimestamp))
	expected := expectedMAC.Sum(nil)
	for _, signature := range signatures {
		got, ok := decodeHMACSignature(signature)
		if !ok {
			continue
		}
		if hmac.Equal(got, expected) {
			return nil
		}
	}
	return ErrInvalidHMACSignature
}

func normalizedSignatures(v HMACVerification) []string {
	out := make([]string, 0, len(v.Signatures)+1)
	if strings.TrimSpace(v.Signature) != "" {
		out = append(out, strings.TrimSpace(v.Signature))
	}
	for _, signature := range v.Signatures {
		for _, item := range strings.Split(signature, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func hmacPayload(body []byte, timestamp string, includeTimestamp bool) []byte {
	if !includeTimestamp {
		return body
	}
	prefix := strings.TrimSpace(timestamp) + "."
	payload := make([]byte, 0, len(prefix)+len(body))
	payload = append(payload, prefix...)
	payload = append(payload, body...)
	return payload
}

func decodeHMACSignature(signature string) ([]byte, bool) {
	signature = strings.TrimSpace(signature)
	if before, after, ok := strings.Cut(signature, "="); ok && strings.EqualFold(strings.TrimSpace(before), "sha256") {
		signature = strings.TrimSpace(after)
	}
	decoded, err := hex.DecodeString(signature)
	return decoded, err == nil && len(decoded) == sha256.Size
}

func parseHMACTimestamp(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, ErrMissingHMACTimestamp
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(unix, 0).UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidHMACTimestamp, err)
	}
	return ts.UTC(), nil
}

func durationAbs(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
