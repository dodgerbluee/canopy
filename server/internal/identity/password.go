// Package identity owns owner credentials and server-side sessions:
// Argon2id password envelopes, opaque session tokens (stored hashed),
// CSRF protection, session expiry, revocation, and login rate limiting.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// MaxPasswordLen bounds hashing work per request (DoS guard).
	MaxPasswordLen = 1024
	keyLen         = 32
	saltLen        = 16
)

// ErrMalformedHash marks an encoded hash that cannot be parsed; callers
// treat it as a verification failure, never as a retryable error.
var ErrMalformedHash = errors.New("malformed password hash")

// Argon2Params are persisted inside every hash, so verification never
// depends on current defaults; raising defaults later only affects new
// hashes (rehash-on-login is a later optimization, not a correctness
// requirement).
type Argon2Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
}

// ProductionParams is the RFC 9106 second recommended option tuned for a
// single-owner host: 64 MiB, 3 iterations, parallelism 2.
var ProductionParams = Argon2Params{Memory: 65536, Iterations: 3, Parallelism: 2}

// cheapParams bounds decode work for hashes we did not create; anything
// larger is rejected before argon2 runs.
var cheapParams = Argon2Params{Memory: 1 << 22, Iterations: 1 << 16, Parallelism: 64}

type PasswordHasher struct {
	Params Argon2Params
	rnd    io.Reader
}

func NewPasswordHasher(p Argon2Params) *PasswordHasher {
	return &PasswordHasher{Params: p, rnd: rand.Reader}
}

// Hash produces $argon2id$v=19$m=,t=,p=$salt$hash (RawStd base64).
func (h *PasswordHasher) Hash(password string) (string, error) {
	if password == "" || len(password) > MaxPasswordLen {
		return "", errors.New("password must be non-empty and at most 1024 bytes")
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(h.rnd, salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.Params.Iterations, h.Params.Memory, h.Params.Parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		h.Params.Memory, h.Params.Iterations, h.Params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword decodes the stored parameters and compares the derived
// key in constant time. It reports false for malformed input rather than
// surfacing parse errors to auth call sites.
func VerifyPassword(encoded, password string) bool {
	if password == "" || len(password) > MaxPasswordLen {
		return false
	}
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(key)))
	return subtle.ConstantTimeCompare(key, got) == 1
}

func decodeHash(s string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(s, "$")
	// leading empty field from the initial '$'
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	var p Argon2Params
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return Argon2Params{}, nil, nil, ErrMalformedHash
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return Argon2Params{}, nil, nil, ErrMalformedHash
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Iterations = uint32(n)
		case "p":
			p.Parallelism = uint8(n)
		default:
			return Argon2Params{}, nil, nil, ErrMalformedHash
		}
	}
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 ||
		p.Memory > cheapParams.Memory || p.Iterations > cheapParams.Iterations ||
		p.Parallelism > cheapParams.Parallelism {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < saltLen {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
