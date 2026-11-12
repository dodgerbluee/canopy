package httpx

import (
	"errors"
	"net/http"

	"canopy/server/internal/identity"
	"canopy/server/internal/platform"
	"github.com/jackc/pgx/v5/pgtype"
)

func (a *Auth) handleOIDCSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	cfg, err := a.oidc.Settings(r.Context(), p.OwnerID)
	if err != nil {
		a.userError(w, r, err)
		return
	}
	writeJSON(w, 200, cfg)
}
func (a *Auth) handleOIDCSave(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	var in identity.OIDCInput
	if !a.decode(w, r, &in) {
		return
	}
	cfg, err := a.oidc.Save(r.Context(), p.OwnerID, p.UserID, in)
	if err != nil {
		if !errors.Is(err, identity.ErrAccess) && !errors.Is(err, identity.ErrConflict) {
			platform.WriteProblem(w, r, a.log, 400, "INVALID_CONNECTION", "Check the issuer, client ID and secret. Changing issuer or client ID requires entering the secret again. SSO requires an HTTPS public URL.")
			return
		}
		a.userError(w, r, err)
		return
	}
	a.log.Info("auth.oidc_settings_changed", "actor_id", uuidString(p.UserID))
	writeJSON(w, 200, cfg)
}
func (a *Auth) handleOIDCTest(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := a.oidc.Test(r.Context(), p.OwnerID); err != nil {
		platform.WriteProblem(w, r, a.log, 400, "CONNECTION_FAILED", "Discovery failed. Check the issuer, TLS certificate and connection settings.")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "Discovery succeeded. Connect your account to verify client credentials and the complete sign-in flow."})
}
func (a *Auth) oidcCookie(w http.ResponseWriter, value string, remove bool) {
	age := 300
	if remove {
		age = -1
	}
	http.SetCookie(w, &http.Cookie{Name: "canopy_oidc", Value: value, Path: "/api/auth/oidc", HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *Auth) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.Allow("oidc:" + clientIP(r)) {
		platform.WriteProblem(w, r, a.log, 429, "RATE_LIMITED", "Too many attempts")
		return
	}
	var owner pgtype.UUID
	if err := a.users.Pool.QueryRow(r.Context(), "SELECT id FROM owner ORDER BY created_at LIMIT 1").Scan(&owner); err != nil {
		http.Redirect(w, r, "/login?oidc=failed", 303)
		return
	}
	target, _, browser, err := a.oidc.Start(r.Context(), owner, pgtype.UUID{}, pgtype.UUID{})
	if err != nil {
		http.Redirect(w, r, "/login?oidc=failed", 303)
		return
	}
	a.oidcCookie(w, browser, false)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, 303)
}
func (a *Auth) handleOIDCLink(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	target, _, browser, err := a.oidc.Start(r.Context(), p.OwnerID, p.UserID, p.SessionID)
	if err != nil {
		platform.WriteProblem(w, r, a.log, 400, "CONNECTION_FAILED", "Could not start SSO. Check the connection and HTTPS public URL.")
		return
	}
	a.oidcCookie(w, browser, false)
	writeJSON(w, 200, map[string]string{"url": target})
}
func (a *Auth) handleOIDCUnlink(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := a.oidc.Unlink(r.Context(), p.OwnerID, p.UserID); err != nil {
		platform.WriteProblem(w, r, a.log, 400, "PASSWORD_REQUIRED", "Set a local password before disconnecting SSO.")
		return
	}
	writeJSON(w, 200, map[string]bool{"disconnected": true})
}
func (a *Auth) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	cookie, err := r.Cookie("canopy_oidc")
	a.oidcCookie(w, "", true)
	if err != nil {
		http.Redirect(w, r, "/login?oidc=failed", 303)
		return
	}
	// The service consumes state once, including failed provider/code responses.
	u, sess, linked, err := a.oidc.Finish(r.Context(), r.URL.Query().Get("state"), cookie.Value, r.URL.Query().Get("code"))
	if err != nil {
		code := "failed"
		if errors.Is(err, identity.ErrNotLinked) {
			code = "unlinked"
		}
		destination := "/login?oidc=" + code
		if current, cookieErr := r.Cookie(a.cfg.CookieName); cookieErr == nil {
			if _, sessionErr := a.sessions.Verify(r.Context(), current.Value); sessionErr == nil {
				destination = "/settings?oidc=failed"
			}
		}
		http.Redirect(w, r, destination, 303)
		return
	}
	a.log.Info("auth.oidc_verified", "user_id", u.ID, "linked", linked)
	if linked {
		http.Redirect(w, r, "/settings?oidc=linked", 303)
		return
	}
	a.setCookie(w, sess.Token)
	http.Redirect(w, r, "/", 303)
}
