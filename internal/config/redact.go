package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Redact renders a token for display: enough to recognise which token it is,
// never enough to use it.
func Redact(token string) string {
	if token == "" {
		return ""
	}
	// Tokens are documented to look like "mbx_pat_xxxxxxxx". Keep the prefix so
	// a user can tell a MYBOX token from something pasted by mistake.
	const keep = 4
	if len(token) <= keep {
		return "****"
	}
	prefix := ""
	if rest, ok := strings.CutPrefix(token, "mbx_pat_"); ok {
		prefix, token = "mbx_pat_", rest
	}
	if len(token) <= keep {
		return prefix + "****"
	}
	return prefix + "****" + token[len(token)-keep:]
}

// Fingerprint derives a short, stable, non-reversible identifier for a token.
// It keys per-account caches so two accounts never read each other's entries.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}
