package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/skymoore/coredns-plugins/admin/store"
	"golang.org/x/oauth2"
)

type oidcRuntime struct {
	provider *oidc.Provider
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newOIDC(ctx context.Context, c oidcSettings) (*oidcRuntime, error) {
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	oc := oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  c.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
	return &oidcRuntime{
		provider: p,
		oauth:    oc,
		verifier: p.Verifier(&oidc.Config{ClientID: c.ClientID}),
	}, nil
}

func (a *Admin) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		writeError(w, http.StatusNotFound, "oidc not configured")
		return
	}
	state, _ := randomHex(16)
	nonce, _ := randomHex(16)
	if err := a.db.PutOIDCState(state, nonce, 10*time.Minute); err != nil {
		writeError(w, http.StatusInternalServerError, "state")
		return
	}
	u := a.oidc.oauth.AuthCodeURL(state, oidc.Nonce(nonce))
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *Admin) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		writeError(w, http.StatusNotFound, "oidc not configured")
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	state := r.URL.Query().Get("state")
	nonce, err := a.db.TakeOIDCState(state)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	tok, err := a.oidc.oauth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "code exchange")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		writeError(w, http.StatusBadRequest, "missing id_token")
		return
	}
	idTok, err := a.oidc.verifier.Verify(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid id_token")
		return
	}
	if idTok.Nonce != nonce {
		writeError(w, http.StatusUnauthorized, "nonce")
		return
	}
	var claims struct {
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Sub               string `json:"sub"`
	}
	_ = idTok.Claims(&claims)
	name := store.NormalizeUsername(claims.Email)
	if name == "" {
		name = store.NormalizeUsername(claims.PreferredUsername)
	}
	if name == "" {
		name = "oidc:" + claims.Sub
	}
	u, err := a.db.GetUserByName(name)
	if err != nil {
		hash, _ := hashPassword(hex.EncodeToString(make([]byte, 16)))
		// Empty DB: first OIDC login is admin (password-off has no bootstrap user).
		role := store.RoleViewer
		n, _ := a.db.UserCount()
		if n == 0 {
			role = store.RoleAdmin
		}
		u = store.User{Username: name, PasswordHash: hash, Role: role}
		if err := a.db.CreateUser(u); err != nil {
			writeError(w, http.StatusInternalServerError, "user")
			return
		}
		u, _ = a.db.GetUserByName(name)
		_, _ = a.db.BumpGeneration()
		go a.pushSnapshot()
	}
	if u.Disabled {
		writeError(w, http.StatusForbidden, "disabled")
		return
	}
	actor := Actor{ID: u.ID, Username: u.Username, Role: u.Role, Kind: "oidc"}
	jwtTok, exp, err := a.issueJWT(actor, jwtTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: jwtTok, Path: "/", Expires: exp,
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"token": jwtTok, "expires_at": exp.Unix(), "user": actor})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
