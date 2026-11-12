// Package httpx carries transport-layer HTTP endpoints: the auth surface,
// the owner-scoped request principal, CSRF/Origin guards and the private
// media/export authorization hooks. Bytes and cookies live here, never in
// the business graph (02-architecture).
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
)

type Auth struct {
	owners   *identity.OwnerService
	sessions *identity.SessionManager
	limiter  *identity.Limiter
	cfg      identity.SessionConfig
	log      *slog.Logger
}

func NewAuth(owners *identity.OwnerService, sessions *identity.SessionManager, limiter *identity.Limiter, cfg identity.SessionConfig, log *slog.Logger) *Auth {
	return &Auth{owners: owners, sessions: sessions, limiter: limiter, cfg: cfg, log: log}
}

// Register mounts the auth surface plus the private-content guards. There
// is deliberately no signup or public reset route. The media/export
// handlers are guards only: later tasks attach byte handlers behind the
// same RequirePrincipal, and 501 is honest PENDING semantics, never a
// success stub.
func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", a.handleLogin)
	mux.HandleFunc("GET /api/auth/session", a.handleSession)
	mux.HandleFunc("POST /api/auth/logout", a.RequirePrincipal(a.handleLogout))
	mux.HandleFunc("POST /api/auth/password", a.RequirePrincipal(a.handlePassword))
	mux.HandleFunc("GET /api/media/{path...}", a.RequirePrincipal(a.notImplemented))
	// guard for every export subroute; the real download handler is
	// mounted on the exact pattern by the composition root and wins here
	mux.HandleFunc("GET /api/exports/{id}/{action...}", a.RequirePrincipal(a.notImplemented))
}

// Principal is the owner-scoped request identity injected by
// RequirePrincipal; downstream tasks read it instead of parsing cookies.
type Principal struct {
	OwnerID     pgtype.UUID
	WorkspaceID pgtype.UUID
	SessionID   pgtype.UUID
	CSRFToken   string
}

type principalKey struct{}

func (a *Auth) RequirePrincipal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.sessionFromRequest(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !a.originOK(r) {
				platform.WriteProblem(w, r, a.log, http.StatusForbidden, "ORIGIN_REJECTED", "Origin rejected")
				return
			}
			if !identity.MatchesCSRF(sess, r.Header.Get("X-CSRF-Token")) {
				platform.WriteProblem(w, r, a.log, http.StatusForbidden, "CSRF_REJECTED", "CSRF token rejected")
				return
			}
		}
		ws, err := a.owners.Workspace(r.Context(), sess.OwnerID)
		if err != nil {
			platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, Principal{
			OwnerID:     sess.OwnerID,
			WorkspaceID: ws.ID,
			SessionID:   sess.ID,
			CSRFToken:   sess.CsrfToken,
		})
		next(w, r.WithContext(ctx))
	}
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		platform.WriteProblem(w, r, a.log, http.StatusForbidden, "ORIGIN_REJECTED", "Origin rejected")
		return
	}
	// JSON-only: form-encoded submissions are the classic CSRF vector.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		platform.WriteProblem(w, r, a.log, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Expected application/json")
		return
	}
	source := clientIP(r)
	if !a.limiter.Allow("ip:"+source) || !a.limiter.Allow("account") {
		platform.WriteProblem(w, r, a.log, http.StatusTooManyRequests, "RATE_LIMITED", "Too many login attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		platform.WriteProblem(w, r, a.log, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	owner, sess, err := a.owners.Login(r.Context(), body.Password)
	if errors.Is(err, identity.ErrInvalidCredential) {
		// generic for unknown owner AND wrong password; no enumeration
		a.owners.LogLoginFailure(source)
		platform.WriteProblem(w, r, a.log, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	}
	if err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	ws, err := a.owners.Workspace(r.Context(), owner.ID)
	if err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	a.setCookie(w, sess.Token)
	a.writeSession(w, sess.Session, sess.CSRFToken, ws.ID)
}

func (a *Auth) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.sessionFromRequest(w, r)
	if !ok {
		return
	}
	ws, err := a.owners.Workspace(r.Context(), sess.OwnerID)
	if err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	// the CSRF synchronizer token is returned only after authentication
	// and must remain retrievable after a page reload (03-contracts)
	a.writeSession(w, sess, sess.CsrfToken, ws.ID)
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := a.sessions.Revoke(r.Context(), p.SessionID, p.OwnerID); err != nil && !errors.Is(err, identity.ErrSessionNotFound) {
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	a.clearCookie(w)
	a.log.Info("auth.logout", "owner_id", uuidString(p.OwnerID), "session_id", uuidString(p.SessionID))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"authenticated":false}`))
}

func (a *Auth) handlePassword(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body")
		return
	}
	_, sess, err := a.owners.ChangePassword(r.Context(), p.OwnerID, body.CurrentPassword, body.NewPassword)
	switch {
	case errors.Is(err, identity.ErrInvalidCredential):
		platform.WriteProblem(w, r, a.log, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials")
		return
	case errors.Is(err, identity.ErrInvalidPassword):
		// generic rejection; the attempted password never reaches client or log
		platform.WriteProblem(w, r, a.log, http.StatusBadRequest, "INVALID_REQUEST", "Password rejected")
		return
	case err != nil:
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return
	}
	a.setCookie(w, sess.Token)
	a.writeSession(w, sess.Session, sess.CSRFToken, p.WorkspaceID)
}

func (a *Auth) notImplemented(w http.ResponseWriter, r *http.Request) {
	platform.WriteProblem(w, r, a.log, http.StatusNotImplemented, "NOT_IMPLEMENTED", "Not implemented")
}

// sessionFromRequest resolves the cookie; on any failure the cookie is
// cleared and a generic 401 written.
func (a *Auth) sessionFromRequest(w http.ResponseWriter, r *http.Request) (dbgen.Session, bool) {
	cookie, err := r.Cookie(a.cfg.CookieName)
	if err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusUnauthorized, "UNAUTHENTICATED", "Not authenticated")
		return dbgen.Session{}, false
	}
	sess, err := a.sessions.Verify(r.Context(), cookie.Value)
	if errors.Is(err, identity.ErrSessionExpired) {
		a.clearCookie(w)
		// SESSION_EXPIRED only reflects what the client itself presented
		// (a stale cookie), so it leaks nothing; the client uses it to
		// explain the redirect after a cold load.
		platform.WriteProblem(w, r, a.log, http.StatusUnauthorized, "SESSION_EXPIRED", "Not authenticated")
		return dbgen.Session{}, false
	}
	if err != nil {
		platform.WriteProblem(w, r, a.log, http.StatusInternalServerError, "INTERNAL", "Internal server error")
		return dbgen.Session{}, false
	}
	return sess, true
}

// originOK enforces strict Origin matching: when a browser sends Origin on
// a state-changing request it must equal this request's scheme://host.
// Non-browser clients omit Origin; SameSite=Lax plus the CSRF synchronizer
// token cover the rest. X-Forwarded-Proto is honored only when the
// operator declared a trusted proxy (CANOPY_TRUST_PROXY=1).
func (a *Auth) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if a.cfg.TrustProxyProto {
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			scheme = p
		}
	}
	return origin == scheme+"://"+r.Host
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrfToken,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	WorkspaceID   string `json:"workspaceId,omitempty"`
}

func (a *Auth) writeSession(w http.ResponseWriter, s dbgen.Session, csrf string, workspaceID pgtype.UUID) {
	expires := ""
	if s.ExpiresAt.Valid {
		expires = s.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, sessionResponse{
		Authenticated: true,
		CSRFToken:     csrf,
		ExpiresAt:     expires,
		WorkspaceID:   uuidString(workspaceID),
	})
}

func (a *Auth) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cfg.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.cfg.AbsoluteTimeout.Seconds()),
	})
}

func (a *Auth) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cfg.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientIP uses the transport address, not X-Forwarded-For: rate-limit
// identity must not be spoofable by the client.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

// WithPrincipal is for trusted in-process adapters after independent identity
// verification. Public HTTP handlers still require their own authentication.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
