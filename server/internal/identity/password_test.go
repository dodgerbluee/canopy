package identity

import (
	"strings"
	"testing"
)

// cheap test params; production calibration is exercised via the decode
// path (params travel inside the hash).
var testParams = Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1}

func TestHashVerifyRoundTrip(t *testing.T) {
	h := NewPasswordHasher(testParams)
	enc, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected envelope: %s", enc)
	}
	if !VerifyPassword(enc, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(enc, "wrong password") {
		t.Fatal("wrong password accepted")
	}
}

func TestParamsStoredAlongsideHash(t *testing.T) {
	// a hash created with one calibration must still verify after the
	// production defaults move on
	h := NewPasswordHasher(Argon2Params{Memory: 128, Iterations: 2, Parallelism: 1})
	enc, err := h.Hash("pw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(enc, "m=128,t=2,p=1") {
		t.Fatalf("params not embedded: %s", enc)
	}
	if !VerifyPassword(enc, "pw") {
		t.Fatal("verification must use embedded params, not current defaults")
	}
}

func TestSaltsDiffer(t *testing.T) {
	h := NewPasswordHasher(testParams)
	a, _ := h.Hash("same")
	b, _ := h.Hash("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical — salt missing")
	}
}

func TestEmptyAndPasswordLimits(t *testing.T) {
	h := NewPasswordHasher(testParams)
	if _, err := h.Hash(""); err == nil {
		t.Fatal("empty password accepted")
	}
	if _, err := h.Hash(strings.Repeat("x", MaxPasswordLen+1)); err == nil {
		t.Fatal("oversized password accepted")
	}
	enc, _ := h.Hash("ok")
	if VerifyPassword(enc, "") {
		t.Fatal("empty password verifies")
	}
}

func TestMalformedHashesRejected(t *testing.T) {
	h := NewPasswordHasher(testParams)
	good, _ := h.Hash("pw")
	cases := []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=64,t=1,p=1$AAAA$AAAA",  // wrong algorithm
		"$argon2id$v=16$m=64,t=1,p=1$AAAA$AAAA", // wrong version
		"$argon2id$v=19$m=64,t=1$AAAA$AAAA",     // missing param
		"$argon2id$v=19$m=64,t=1,p=1,x=9$AAAA$AAAA",    // unknown param
		"$argon2id$v=19$m=0,t=1,p=1$AAAA$AAAA",         // zero memory
		"$argon2id$v=19$m=999999999,t=1,p=1$AAAA$AAAA", // DoS-sized memory
		"$argon2id$v=19$m=64,t=1,p=1$not-base64!$AAAA", // bad salt
	}
	// tampered key must fail while the envelope stays parseable.
	// The last base64url char of a 43-char (32-byte) tag carries only
	// 4 data bits — its low 2 bits are ignored padding — so a naive
	// A->B flip can decode to identical bytes (latent flake). Toggle a
	// bit that is guaranteed to change the decoded tag instead.
	const b64url = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := good[len(good)-1]
	v := strings.IndexByte(b64url, last)
	if v < 0 {
		t.Fatalf("hash tail %q is not base64url", last)
	}
	flip := b64url[v^0b010000]
	tampered := good[:len(good)-1] + string(flip)
	cases = append(cases, tampered)
	for _, c := range cases {
		if VerifyPassword(c, "pw") {
			t.Errorf("accepted malformed/tampered hash %q", c)
		}
	}
}
