package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5/pgtype"
)

type testCode struct{ nonce, challenge string }
type testIssuer struct {
	t       *testing.T
	srv     *httptest.Server
	key     *rsa.PrivateKey
	mu      sync.Mutex
	codes   map[string]testCode
	mode    string
	subject string
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &testIssuer{t: t, key: key, codes: map[string]testCode{}, subject: "person-123"}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}
func (p *testIssuer) issuer() string { return p.srv.URL + "/realm/" }
func (p *testIssuer) issue(target string) string {
	p.t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		p.t.Fatal(err)
	}
	v := u.Query()
	if v.Get("code_challenge_method") != "S256" || v.Get("nonce") == "" || v.Get("state") == "" {
		p.t.Fatal("missing PKCE/state/nonce")
	}
	code, _ := randomToken(rand.Reader)
	p.mu.Lock()
	p.codes[code] = testCode{v.Get("nonce"), v.Get("code_challenge")}
	p.mu.Unlock()
	return code
}
func (p *testIssuer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/realm/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.issuer(), "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/keys":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "test", Use: "sig", Algorithm: "RS256"}}})
	case "/token":
		_ = r.ParseForm()
		id, secret, ok := r.BasicAuth()
		if !ok {
			id = r.Form.Get("client_id")
			secret = r.Form.Get("client_secret")
		}
		if id != "canopy-client" || secret != "private-client-secret" {
			http.Error(w, "invalid client", 401)
			return
		}
		p.mu.Lock()
		flow, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		mode, subject := p.mode, p.subject
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || flow.challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
			http.Error(w, "invalid PKCE", 400)
			return
		}
		claims := map[string]any{"iss": p.issuer(), "aud": "canopy-client", "sub": subject, "nonce": flow.nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "name": "SSO gardener", "email": "gardener@example.com", "email_verified": true}
		switch mode {
		case "nonce":
			claims["nonce"] = "wrong"
		case "issuer":
			claims["iss"] = "https://attacker.invalid"
		case "audience":
			claims["aud"] = "another-client"
		case "expiry":
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		}
		key := p.key
		if mode == "signature" {
			key, _ = rsa.GenerateKey(rand.Reader, 2048)
		}
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test"))
		if err != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		data, _ := json.Marshal(claims)
		signed, _ := signer.Sign(data)
		token, _ := signed.CompactSerialize()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "discarded-token", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
	default:
		http.NotFound(w, r)
	}
}
func oidcFixture(t *testing.T, auto bool) (*OIDC, *testIssuer, User) {
	t.Helper()
	users, admin := accountFixture(t)
	p := newTestIssuer(t)
	s, err := NewOIDC(users, filepath.Join(t.TempDir(), "oidc.key"), "http://localhost:7080")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Save(context.Background(), parseTestID(admin.ID), parseTestID(admin.ID), OIDCInput{DisplayName: "Test SSO", Issuer: p.issuer(), ClientID: "canopy-client", ClientSecret: "private-client-secret", Enabled: true, AutoJoin: auto})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, admin
}
func beginOIDCTest(t *testing.T, s *OIDC, p *testIssuer, owner, user, session pgtype.UUID) (state, browser, code string) {
	t.Helper()
	target, state, browser, err := s.Start(context.Background(), owner, user, session)
	if err != nil {
		t.Fatal(err)
	}
	return state, browser, p.issue(target)
}
func TestOIDCExplicitLinkThenLogin(t *testing.T) {
	s, p, admin := oidcFixture(t, false)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	state, browser, code := beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, _, _, err := s.Finish(ctx, state, browser, code); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("unknown identity granted access: %v", err)
	}
	_, local, err := s.Users.Login(ctx, "gardener", "garden-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, owner, local.Session.ID)
	u, _, linked, err := s.Finish(ctx, state, browser, code)
	if err != nil || !linked || u.ID != admin.ID {
		t.Fatalf("link: %+v %v %v", u, linked, err)
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	u, sess, linked, err := s.Finish(ctx, state, browser, code)
	if err != nil || linked || u.ID != admin.ID || sess.Token == "" {
		t.Fatalf("SSO login: %+v %v", u, err)
	}
	if _, _, _, err = s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatalf("callback replay accepted: %v", err)
	}
	if err = s.Unlink(ctx, owner, owner); err != nil {
		t.Fatal(err)
	}
}
func TestOIDCAutoJoinMemberAndDisabledCannotRejoin(t *testing.T) {
	s, p, admin := oidcFixture(t, true)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	state, browser, code := beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	// A different browser cannot redeem the callback or consume the real attempt.
	if _, _, _, err := s.Finish(ctx, state, "other-browser", code); !errors.Is(err, ErrOIDC) {
		t.Fatal("browser binding bypassed")
	}
	u, sess, _, err := s.Finish(ctx, state, browser, code)
	if err != nil || u.Role != "MEMBER" || u.HasPassword {
		t.Fatalf("auto join: %+v %v", u, err)
	}
	if _, err = s.Users.Edit(ctx, owner, owner, parseTestID(u.ID), UserInput{Username: u.Username, DisplayName: u.DisplayName, Role: "MEMBER", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Users.Sessions.Verify(ctx, sess.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatal("disabled SSO session survived")
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, _, _, err = s.Finish(ctx, state, browser, code); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("disabled identity rejoined: %v", err)
	}
}
func TestOIDCRejectsInvalidVerifiedClaims(t *testing.T) {
	for _, mode := range []string{"nonce", "issuer", "audience", "expiry", "signature"} {
		t.Run(mode, func(t *testing.T) {
			s, p, admin := oidcFixture(t, true)
			p.mode = mode
			state, browser, code := beginOIDCTest(t, s, p, parseTestID(admin.ID), pgtype.UUID{}, pgtype.UUID{})
			if _, _, _, err := s.Finish(context.Background(), state, browser, code); !errors.Is(err, ErrOIDC) {
				t.Fatalf("invalid %s accepted: %v", mode, err)
			}
			users, err := s.Users.List(context.Background(), parseTestID(admin.ID))
			if err != nil || len(users) != 1 {
				t.Fatal("invalid token created an account")
			}
		})
	}
}
func TestOIDCRejectsExpiredOrChangedAttemptsAndRevokedLink(t *testing.T) {
	s, p, admin := oidcFixture(t, true)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	state, browser, code := beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, err := s.Users.Pool.Exec(ctx, "UPDATE oidc_attempts SET expires_at=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatal("expired attempt accepted")
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, err := s.Users.Pool.Exec(ctx, "UPDATE oidc_connections SET revision=revision+1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatal("stale configuration accepted")
	}
	_, local, err := s.Users.Login(ctx, "gardener", "garden-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, owner, local.Session.ID)
	if err = s.Users.Sessions.Revoke(ctx, local.Session.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatal("revoked link session accepted")
	}
}
func TestOIDCSecretEncryptedAndNeverReturned(t *testing.T) {
	s, _, admin := oidcFixture(t, true)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	var ciphertext []byte
	if err := s.Users.Pool.QueryRow(ctx, "SELECT secret FROM oidc_connections").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "private-client-secret" {
		t.Fatal("plaintext client secret in DB")
	}
	cfg, err := s.Settings(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if !cfg.HasSecret || strings.Contains(string(data), "private-client-secret") {
		t.Fatal("bad public connection")
	}
	if err = s.Test(ctx, owner); err != nil {
		t.Fatal(err)
	}
}

func TestOIDCRejectsBrokenPKCEAndDeniedFlowIsConsumed(t *testing.T) {
	s, p, admin := oidcFixture(t, true)
	ctx := context.Background()
	owner := parseTestID(admin.ID)
	state, browser, code := beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, err := s.Users.Pool.Exec(ctx, "UPDATE oidc_attempts SET verifier='tampered-verifier' WHERE state_hash=$1", HashToken(state)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatal("invalid PKCE exchange accepted")
	}
	state, browser, code = beginOIDCTest(t, s, p, owner, pgtype.UUID{}, pgtype.UUID{})
	if _, _, _, err := s.Finish(ctx, state, browser, ""); !errors.Is(err, ErrOIDC) {
		t.Fatal("provider cancellation accepted")
	}
	if _, _, _, err := s.Finish(ctx, state, browser, code); !errors.Is(err, ErrOIDC) {
		t.Fatal("cancelled attempt could be replayed")
	}
}
