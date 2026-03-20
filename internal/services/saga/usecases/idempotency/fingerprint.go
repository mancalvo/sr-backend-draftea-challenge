package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RequestFingerprint computes a stable SHA-256 hex digest of the canonical JSON representation.
func RequestFingerprint(cmd any) string {
	b, _ := json.Marshal(cmd)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
