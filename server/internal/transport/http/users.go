package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	dbgen "canopy/server/db/generated"
	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnableUsers keeps the existing auth constructor usable by isolated adapters.
func (a *Auth) EnableUsers(mux *http.ServeMux, users *identity.Users, oidc *identity.OIDC) {
	a.users = users
	a.oidc = oidc
	mux.HandleFunc("GET /api/auth/options", a.handleOptions)
	mux.HandleFunc("POST /api/auth/setup", a.handleSetup)
	mux.HandleFunc("POST /api/auth/setup/claim", a.handleClaimExchange)
	mux.HandleFunc("GET /api/auth/me", a.RequirePrincipal(a.handleMe))
	mux.HandleFunc("GET /api/auth/users", a.admin(a.handleUsers))
	mux.HandleFunc("POST /api/auth/users", a.admin(a.handleUserEdit))
	mux.HandleFunc("PUT /api/auth/users/{id}", a.admin(a.handleUserEdit))
	mux.HandleFunc("GET /api/auth/oidc/settings", a.admin(a.handleOIDCSettings))
	mux.HandleFunc("PUT /api/auth/oidc/settings", a.admin(a.handleOIDCSave))
	mux.HandleFunc("POST /api/auth/oidc/test", a.admin(a.handleOIDCTest))
	mux.HandleFunc("GET /api/auth/oidc/start", a.handleOIDCStart)
	mux.HandleFunc("POST /api/auth/oidc/link", a.RequirePrincipal(a.handleOIDCLink))
	mux.HandleFunc("POST /api/auth/oidc/unlink", a.RequirePrincipal(a.handleOIDCUnlink))
	mux.HandleFunc("GET /api/auth/oidc/callback", a.handleOIDCCallback)
}
func (a *Auth) admin(next http.HandlerFunc) http.HandlerFunc {
	return a.RequirePrincipal(func(w http.ResponseWriter, r *http.Request) {
		p, _ := PrincipalFrom(r.Context())
		if p.Role != "ADMIN" {
			a.userError(w, r, identity.ErrAccess)
			return
		}
		next(w, r)
	})
}
func (a *Auth) decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		platform.WriteProblem(w, r, a.log, 415, "UNSUPPORTED_MEDIA_TYPE", "Expected application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		a.userError(w, r, identity.ErrInput)
		return false
	}
	return true
}
func (a *Auth) userError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 500, "INTERNAL", "The request could not be completed."
	switch {
	case errors.Is(err, identity.ErrHandover):
		status, code, message = 400, "INVALID_REGISTRATION_LINK", err.Error()
	case errors.Is(err, identity.ErrAccess):
		status, code, message = 403, "FORBIDDEN", err.Error()
	case errors.Is(err, identity.ErrOwnerExists):
		status, code, message = 409, "SETUP_COMPLETE", "Setup is already complete. Sign in instead."
	case errors.Is(err, identity.ErrLastAdmin):
		status, code, message = 409, "LAST_ADMIN", err.Error()
	case errors.Is(err, identity.ErrConflict):
		status, code, message = 409, "CONFLICT", err.Error()
	case errors.Is(err, identity.ErrInput):
		status, code, message = 400, "INVALID_REQUEST", err.Error()
	case errors.Is(err, identity.ErrInvalidCredential):
		status, code, message = 401, "INVALID_CREDENTIALS", "Invalid credentials"
	}
	platform.WriteProblem(w, r, a.log, status, code, message)
}
func (a *Auth) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if cookie, err := r.Cookie("canopy_registration"); err == nil {
		available, checkErr := a.users.HandoverAvailable(r.Context(), cookie.Value)
		if checkErr != nil {
			a.userError(w, r, checkErr)
			return
		}
		if available {
			writeJSON(w, 200, map[string]any{"setupRequired": true, "claimExisting": true, "oidcName": ""})
			return
		}
		a.registrationCookie(w, "", true)
	}
	setup, err := a.users.SetupAvailable(r.Context())
	if err != nil {
		a.userError(w, r, err)
		return
	}
	name := ""
	if !setup {
		var owner pgtype.UUID
		err = a.users.Pool.QueryRow(r.Context(), "SELECT id FROM owner ORDER BY created_at LIMIT 1").Scan(&owner)
		if err != nil {
			a.userError(w, r, err)
			return
		}
		cfg, err := a.oidc.Settings(r.Context(), owner)
		if err != nil {
			a.userError(w, r, err)
			return
		}
		if cfg.Enabled {
			name = cfg.DisplayName
		}
	}
	writeJSON(w, 200, map[string]any{"setupRequired": setup, "oidcName": name})
}
func (a *Auth) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		platform.WriteProblem(w, r, a.log, 403, "ORIGIN_REJECTED", "Origin rejected")
		return
	}
	if !a.limiter.Allow("setup:" + clientIP(r)) {
		platform.WriteProblem(w, r, a.log, 429, "RATE_LIMITED", "Too many attempts")
		return
	}
	var in identity.SetupInput
	if !a.decode(w, r, &in) {
		return
	}
	var u identity.User
	var ws dbgen.Workspace
	var err error
	if cookie, cookieErr := r.Cookie("canopy_registration"); cookieErr == nil {
		u, ws, err = a.users.RegisterExisting(r.Context(), cookie.Value, in)
	} else {
		u, ws, err = a.users.Setup(r.Context(), in)
	}
	if err != nil {
		a.userError(w, r, err)
		return
	}
	sess, err := a.sessions.NewForUser(r.Context(), ws.OwnerID, asUUID(u.ID))
	if err != nil {
		a.userError(w, r, err)
		return
	}
	a.registrationCookie(w, "", true)
	a.setCookie(w, sess.Token)
	a.writeSession(r.Context(), w, sess.Session, sess.CSRFToken, ws.ID)
	a.log.Info("auth.setup_complete", "user_id", u.ID)
}
func (a *Auth) handleMe(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	u, err := a.users.ByID(r.Context(), p.OwnerID, p.UserID)
	if err != nil {
		a.userError(w, r, err)
		return
	}
	writeJSON(w, 200, u)
}
func (a *Auth) handleUsers(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	users, err := a.users.List(r.Context(), p.OwnerID)
	if err != nil {
		a.userError(w, r, err)
		return
	}
	writeJSON(w, 200, users)
}
func (a *Auth) handleUserEdit(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	var id pgtype.UUID
	if r.Method == http.MethodPut {
		id = asUUID(r.PathValue("id"))
		if !id.Valid {
			a.userError(w, r, identity.ErrInput)
			return
		}
	}
	var in identity.UserInput
	if !a.decode(w, r, &in) {
		return
	}
	u, err := a.users.Edit(r.Context(), p.OwnerID, p.UserID, id, in)
	if err != nil {
		a.userError(w, r, err)
		return
	}
	a.log.Info("auth.user_changed", "actor_id", uuidString(p.UserID), "user_id", u.ID)
	writeJSON(w, 200, u)
}
func asUUID(s string) pgtype.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func (a *Auth) registrationCookie(w http.ResponseWriter, token string, clear bool) {
	age := 3600
	if clear {
		age = -1
	}
	http.SetCookie(w, &http.Cookie{Name: "canopy_registration", Value: token, Path: "/api/auth", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (a *Auth) handleClaimExchange(w http.ResponseWriter, r *http.Request) {
	if !a.originOK(r) {
		platform.WriteProblem(w, r, a.log, 403, "ORIGIN_REJECTED", "Origin rejected")
		return
	}
	if !a.limiter.Allow("registration:" + clientIP(r)) {
		platform.WriteProblem(w, r, a.log, 429, "RATE_LIMITED", "Too many attempts")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	available, err := a.users.HandoverAvailable(r.Context(), in.Token)
	if err != nil {
		a.userError(w, r, err)
		return
	}
	if !available {
		a.userError(w, r, identity.ErrHandover)
		return
	}
	a.registrationCookie(w, in.Token, false)
	writeJSON(w, 200, map[string]bool{"ready": true})
}
