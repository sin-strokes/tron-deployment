package security

import "fmt"

const redacted = "[REDACTED]"

// PrivateKey wraps a private key string to prevent accidental leakage.
// String() and MarshalJSON() always return "[REDACTED]".
// Use Value() only when the raw key is genuinely needed (e.g., passing to systemd env).
type PrivateKey struct {
	raw string
}

// NewPrivateKey creates a PrivateKey from a raw string.
func NewPrivateKey(raw string) PrivateKey {
	return PrivateKey{raw: raw}
}

// Value returns the actual private key. Use with extreme care.
func (k PrivateKey) Value() string {
	return k.raw
}

// String always returns "[REDACTED]" to prevent accidental logging.
func (k PrivateKey) String() string {
	return redacted
}

// GoString returns "[REDACTED]" for %#v formatting.
func (k PrivateKey) GoString() string {
	return fmt.Sprintf("security.PrivateKey{%s}", redacted)
}

// MarshalJSON returns "[REDACTED]" as a JSON string.
func (k PrivateKey) MarshalJSON() ([]byte, error) {
	return []byte(`"` + redacted + `"`), nil
}

// MarshalText returns "[REDACTED]" for text marshaling (used by YAML, etc).
func (k PrivateKey) MarshalText() ([]byte, error) {
	return []byte(redacted), nil
}
