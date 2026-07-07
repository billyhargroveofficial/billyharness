package agentclub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func SignWebhookHMACSHA256(secret, body []byte, timestamp string, includeTimestamp bool) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(hmacPayload(body, timestamp, includeTimestamp))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
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
