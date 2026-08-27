package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/skymoore/coredns-ez/admin/store"
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

// oidcProvisionRole is the role for a newly created OIDC user.
// Empty DB → admin. Password login on → later OIDC users are viewers.
// Password off → leftover bootstrap_admin cannot sign in, so the first
// federated user is admin (installer seeds that local user before OIDC).
func oidcProvisionRole(passwordOn bool, bootstrap string, users []store.User) string {
	boot := store.NormalizeUsername(bootstrap)
	if boot == "" {
		boot = "admin"
	}
	if len(users) == 0 {
		return store.RoleAdmin
	}
	if passwordOn {
		return store.RoleViewer
	}
	for _, u := range users {
		if u.Disabled || u.Role != store.RoleAdmin {
			continue
		}
		if store.NormalizeUsername(u.Username) != boot {
			return store.RoleViewer
		}
	}
	return store.RoleAdmin
}

func (a *Admin) oidcRedirectURL(r *http.Request) string {
	if a.cfg.OIDC != nil && strings.TrimSpace(a.cfg.OIDC.RedirectURL) != "" {
		return a.cfg.OIDC.RedirectURL
	}
	return requestBaseURL(r) + "/api/v1/auth/oidc/callback"
}

func (a *Admin) oauthForRequest(r *http.Request) oauth2.Config {
	oc := a.oidc.oauth
	oc.RedirectURL = a.oidcRedirectURL(r)
	return oc
}

func (a *Admin) reloadOIDCFromDB() {
	if a.cfg.OIDC != nil {
		return
	}
	oc, err := a.db.GetOIDC()
	if err != nil {
		return
	}
	rt, err := newOIDC(context.Background(), oidcSettings{
		Issuer: oc.Issuer, ClientID: oc.ClientID, ClientSecret: oc.ClientSecret,
		RedirectURL: oc.RedirectURL, ButtonText: oc.ButtonText, ButtonImage: oc.ButtonImage,
	})
	if err != nil {
		log.Warningf("oidc reload: %v", err)
		return
	}
	a.oidc = rt
}

func (a *Admin) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.reloadOIDCFromDB()
	}
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
	oc := a.oauthForRequest(r)
	u := oc.AuthCodeURL(state, oidc.Nonce(nonce))
	http.Redirect(w, r, u, http.StatusFound)
}

func (a *Admin) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		a.reloadOIDCFromDB()
	}
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
	oc := a.oauthForRequest(r)
	tok, err := oc.Exchange(r.Context(), r.URL.Query().Get("code"))
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
		users, _ := a.db.ListUsers()
		role := oidcProvisionRole(a.cfg.Password, a.cfg.BootstrapAdmin, users)
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
	// Authentik (and every other IdP) lands the browser on this URL. JSON
	// here is a dead end; send humans to the UI. API clients that ask for
	// JSON still get the token body.
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"token": jwtTok, "expires_at": exp.Unix(), "user": actor})
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func wantsJSON(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return strings.Contains(a, "application/json") && !strings.Contains(a, "text/html")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
