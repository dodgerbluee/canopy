package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"canopy/server/internal/platform"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
)

var ErrOIDC = errors.New("sign-in could not be verified; try again or use your password")
var ErrNotLinked = errors.New("this SSO identity is not linked; sign in with your password and connect it in Settings")

type OIDCSettings struct {
	DisplayName string `json:"displayName"`
	Issuer      string `json:"issuer"`
	ClientID    string `json:"clientId"`
	HasSecret   bool   `json:"hasSecret"`
	Enabled     bool   `json:"enabled"`
	AutoJoin    bool   `json:"autoJoin"`
	Revision    int    `json:"revision"`
	CallbackURL string `json:"callbackUrl"`
}
type OIDCInput struct {
	DisplayName  string `json:"displayName"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Enabled      bool   `json:"enabled"`
	AutoJoin     bool   `json:"autoJoin"`
	Revision     int    `json:"revision"`
}
type OIDC struct {
	Users     *Users
	PublicURL string
	box       *credentialBox
	Client    *http.Client
}

func NewOIDC(users *Users, keyPath, publicURL string) (*OIDC, error) {
	publicURL = strings.TrimSuffix(publicURL, "/")
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("CANOPY_PUBLIC_URL must be an HTTP or HTTPS origin")
	}
	box, err := loadCredentialBox(keyPath)
	if err != nil {
		return nil, err
	}
	return &OIDC{Users: users, PublicURL: publicURL, box: box, Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Local HTTP deployments can keep using passwords. OIDC itself requires a secure
// public origin, except for loopback development.
func validOIDCURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}
func (s *OIDC) callback() string { return s.PublicURL + "/api/auth/oidc/callback" }
func (s *OIDC) Settings(ctx context.Context, owner pgtype.UUID) (out OIDCSettings, err error) {
	out.CallbackURL = s.callback()
	err = s.Users.Pool.QueryRow(ctx, `SELECT display_name,issuer,client_id,octet_length(secret)>0,enabled,auto_join,revision FROM oidc_connections WHERE owner_id=$1`, owner).Scan(&out.DisplayName, &out.Issuer, &out.ClientID, &out.HasSecret, &out.Enabled, &out.AutoJoin, &out.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return
}
func (s *OIDC) Save(ctx context.Context, owner, actor pgtype.UUID, in OIDCInput) (OIDCSettings, error) {
	in.Issuer = strings.TrimSpace(in.Issuer)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.ClientID = strings.TrimSpace(in.ClientID)
	if !validOIDCURL(in.Issuer) || len(in.Issuer) > 2048 || len(in.DisplayName) == 0 || len(in.DisplayName) > 80 || len(in.ClientID) == 0 || len(in.ClientID) > 1024 || len(in.ClientSecret) > 8192 {
		return OIDCSettings{}, ErrInput
	}
	if in.Enabled && !validOIDCURL(s.PublicURL) {
		return OIDCSettings{}, errors.New("configure CANOPY_PUBLIC_URL with HTTPS before enabling SSO")
	}
	tx, err := s.Users.Pool.Begin(ctx)
	if err != nil {
		return OIDCSettings{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM owner WHERE id=$1 FOR UPDATE", owner); err != nil {
		return OIDCSettings{}, err
	}
	var admin bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM auth_users WHERE id=$1 AND owner_id=$2 AND role='ADMIN' AND NOT disabled)", actor, owner).Scan(&admin); err != nil {
		return OIDCSettings{}, err
	}
	if !admin {
		return OIDCSettings{}, ErrAccess
	}
	var secret []byte
	var revision int
	var oldIssuer, oldClient string
	err = tx.QueryRow(ctx, "SELECT secret,revision,issuer,client_id FROM oidc_connections WHERE owner_id=$1", owner).Scan(&secret, &revision, &oldIssuer, &oldClient)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return OIDCSettings{}, err
	}
	if revision != in.Revision {
		return OIDCSettings{}, ErrConflict
	}
	if in.ClientSecret != "" {
		secret, err = s.box.seal(in.ClientSecret, uuidString(owner))
		if err != nil {
			return OIDCSettings{}, err
		}
	}
	if len(secret) == 0 || (in.ClientSecret == "" && revision > 0 && (oldIssuer != in.Issuer || oldClient != in.ClientID)) {
		return OIDCSettings{}, errors.New("enter the client secret for this connection")
	}
	_, err = tx.Exec(ctx, `INSERT INTO oidc_connections(owner_id,display_name,issuer,client_id,secret,enabled,auto_join) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(owner_id) DO UPDATE SET display_name=$2,issuer=$3,client_id=$4,secret=$5,enabled=$6,auto_join=$7,revision=oidc_connections.revision+1`, owner, in.DisplayName, in.Issuer, in.ClientID, secret, in.Enabled, in.AutoJoin)
	if err != nil {
		return OIDCSettings{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return OIDCSettings{}, err
	}
	return s.Settings(ctx, owner)
}
func (s *OIDC) config(ctx context.Context, owner pgtype.UUID) (OIDCSettings, string, error) {
	cfg, err := s.Settings(ctx, owner)
	if err != nil || cfg.Revision == 0 {
		return cfg, "", ErrOIDC
	}
	var enc []byte
	if err = s.Users.Pool.QueryRow(ctx, "SELECT secret FROM oidc_connections WHERE owner_id=$1 AND revision=$2", owner, cfg.Revision).Scan(&enc); err != nil {
		return cfg, "", ErrOIDC
	}
	secret, err := s.box.open(enc, uuidString(owner))
	return cfg, secret, err
}
func (s *OIDC) provider(ctx context.Context, cfg OIDCSettings, secret string) (*oidc.Provider, oauth2.Config, error) {
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, s.Client), cfg.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, ErrOIDC
	}
	ep := p.Endpoint()
	var metadata struct {
		JWKS string `json:"jwks_uri"`
	}
	if err = p.Claims(&metadata); err != nil {
		return nil, oauth2.Config{}, ErrOIDC
	}
	if !validOIDCURL(ep.AuthURL) || !validOIDCURL(ep.TokenURL) || !validOIDCURL(metadata.JWKS) {
		return nil, oauth2.Config{}, ErrOIDC
	}
	return p, oauth2.Config{ClientID: cfg.ClientID, ClientSecret: secret, RedirectURL: s.callback(), Endpoint: ep, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}, nil
}
func (s *OIDC) Test(ctx context.Context, owner pgtype.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cfg, secret, err := s.config(ctx, owner)
	if err != nil {
		return err
	}
	_, _, err = s.provider(ctx, cfg, secret)
	return err
}
func (s *OIDC) Start(ctx context.Context, owner, user, session pgtype.UUID) (authURL, state, browser string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !validOIDCURL(s.PublicURL) {
		err = ErrOIDC
		return
	}
	cfg, secret, err := s.config(ctx, owner)
	if err != nil {
		return "", "", "", err
	}
	if !user.Valid && !cfg.Enabled {
		return "", "", "", ErrOIDC
	}
	_, client, err := s.provider(ctx, cfg, secret)
	if err != nil {
		return "", "", "", err
	}
	state, err = randomToken(rand.Reader)
	if err != nil {
		return
	}
	browser, err = randomToken(rand.Reader)
	if err != nil {
		return
	}
	nonce, err := randomToken(rand.Reader)
	if err != nil {
		return
	}
	verifier := oauth2.GenerateVerifier()
	_, err = s.Users.Pool.Exec(ctx, "DELETE FROM oidc_attempts WHERE expires_at<now()")
	if err != nil {
		return
	}
	_, err = s.Users.Pool.Exec(ctx, `INSERT INTO oidc_attempts(state_hash,browser_hash,owner_id,user_id,session_id,nonce,verifier,revision,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()+interval '5 minutes')`, HashToken(state), HashToken(browser), owner, user, session, nonce, verifier, cfg.Revision)
	if err != nil {
		return
	}
	authURL = client.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return
}
func (s *OIDC) Finish(ctx context.Context, state, browser, code string) (User, IssuedSession, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if state == "" || browser == "" || len(state) > 128 || len(browser) > 128 || len(code) > 8192 {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	var owner, user, session pgtype.UUID
	var nonce, verifier string
	var revision int
	err := s.Users.Pool.QueryRow(ctx, `DELETE FROM oidc_attempts WHERE state_hash=$1 AND browser_hash=$2 AND expires_at>now() RETURNING owner_id,user_id,session_id,nonce,verifier,revision`, HashToken(state), HashToken(browser)).Scan(&owner, &user, &session, &nonce, &verifier, &revision)
	if err != nil {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	if code == "" {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	cfg, secret, err := s.config(ctx, owner)
	if err != nil || cfg.Revision != revision || (!user.Valid && !cfg.Enabled) {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	p, client, err := s.provider(ctx, cfg, secret)
	if err != nil {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	token, err := client.Exchange(oidc.ClientContext(ctx, s.Client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	verified, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(oidc.ClientContext(ctx, s.Client), raw)
	if err != nil || verified.Subject == "" || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(nonce)) != 1 {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	tx, err := s.Users.Pool.Begin(ctx)
	if err != nil {
		return User{}, IssuedSession{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM owner WHERE id=$1 FOR UPDATE", owner); err != nil {
		return User{}, IssuedSession{}, false, err
	}
	var current int
	if err = tx.QueryRow(ctx, "SELECT revision FROM oidc_connections WHERE owner_id=$1", owner).Scan(&current); err != nil || current != revision {
		return User{}, IssuedSession{}, false, ErrOIDC
	}
	linking := user.Valid
	if linking {
		var live bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions s JOIN auth_users u ON u.id=$3 AND u.owner_id=$2 WHERE s.id=$1 AND s.owner_id=$2 AND coalesce(s.user_id,s.owner_id)=u.id AND s.revoked_at IS NULL AND s.expires_at>now() AND coalesce(s.last_seen_at,s.created_at)>now()-interval '12 hours' AND NOT u.disabled)`, session, owner, user).Scan(&live)
		if err != nil || !live {
			return User{}, IssuedSession{}, false, ErrOIDC
		}
		var linked pgtype.UUID
		err = tx.QueryRow(ctx, "SELECT user_id FROM oidc_identities WHERE issuer=$1 AND subject=$2", cfg.Issuer, verified.Subject).Scan(&linked)
		if err == nil && linked != user {
			return User{}, IssuedSession{}, false, ErrConflict
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return User{}, IssuedSession{}, false, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)
 ON CONFLICT(issuer,subject) DO UPDATE SET user_id=oidc_identities.user_id
 WHERE oidc_identities.user_id=EXCLUDED.user_id RETURNING user_id`, cfg.Issuer, verified.Subject, user).Scan(&linked)
		if err != nil || linked != user {
			return User{}, IssuedSession{}, false, ErrConflict
		}
	} else {
		err = tx.QueryRow(ctx, "SELECT u.id FROM oidc_identities i JOIN auth_users u ON u.id=i.user_id WHERE i.issuer=$1 AND i.subject=$2 AND u.owner_id=$3 AND NOT u.disabled", cfg.Issuer, verified.Subject, owner).Scan(&user)
		if errors.Is(err, pgx.ErrNoRows) {
			// A disabled identity is never silently replaced with a fresh account.
			var bound bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oidc_identities WHERE issuer=$1 AND subject=$2)", cfg.Issuer, verified.Subject).Scan(&bound); err != nil {
				return User{}, IssuedSession{}, false, err
			}
			if bound || !cfg.AutoJoin {
				return User{}, IssuedSession{}, false, ErrNotLinked
			}
			user = platform.UUIDv4{}.New()
			var claims struct {
				Name string `json:"name"`
			}
			_ = verified.Claims(&claims)
			name := strings.TrimSpace(claims.Name)
			if name == "" || len(name) > 160 {
				name = "Garden member"
			}
			_, err = tx.Exec(ctx, "INSERT INTO auth_users(id,owner_id,username,display_name,role) VALUES($1,$2,$3,$4,'MEMBER')", user, owner, "sso-"+strings.ReplaceAll(uuidString(user), "-", ""), name)
			if err != nil {
				return User{}, IssuedSession{}, false, err
			}
			_, err = tx.Exec(ctx, "INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)", cfg.Issuer, verified.Subject, user)
		}
		if err != nil {
			return User{}, IssuedSession{}, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return User{}, IssuedSession{}, false, err
	}
	u, err := s.Users.ByID(ctx, owner, user)
	if err != nil {
		return User{}, IssuedSession{}, false, err
	}
	if linking {
		return u, IssuedSession{}, true, nil
	}
	sess, err := s.Users.Sessions.NewForUser(ctx, owner, user)
	return u, sess, false, err
}
func (s *OIDC) Unlink(ctx context.Context, owner, user pgtype.UUID) error {
	u, err := s.Users.ByID(ctx, owner, user)
	if err != nil {
		return err
	}
	if !u.HasPassword {
		return errors.New("ask an administrator to set a local password before disconnecting SSO")
	}
	_, err = s.Users.Pool.Exec(ctx, "DELETE FROM oidc_identities WHERE user_id=$1", user)
	return err
}
