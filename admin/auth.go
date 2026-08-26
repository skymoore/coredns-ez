package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/skymoore/coredns-plugins/admin/store"
)

type ctxKey int

const actorKey ctxKey = 1

type Actor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Kind     string `json:"kind"`
}

type jwtClaims struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	jwt.RegisteredClaims
}

func (a *Admin) hmac() []byte {
	v, _ := a.db.Meta(store.MetaJWTHMAC)
	return []byte(v)
}

func (a *Admin) issueJWT(actor Actor, ttl time.Duration) (string, time.Time, error) {
	exp := time.Now().Add(ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims{
		Role: actor.Role,
		Kind: actor.Kind,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   actor.ID,
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	s, err := tok.SignedString(a.hmac())
	return s, exp, err
}

func (a *Admin) parseJWT(raw string) (Actor, error) {
	tok, err := jwt.ParseWithClaims(raw, &jwtClaims{}, func(t *jwt.Token) (any, error) {
		return a.hmac(), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return Actor{}, err
	}
	c, ok := tok.Claims.(*jwtClaims)
	if !ok || !tok.Valid {
		return Actor{}, jwt.ErrTokenInvalidClaims
	}
	u, err := a.db.GetUser(c.Subject)
	if err != nil {
		return Actor{}, err
	}
	if u.Disabled {
		return Actor{}, errUnauthorized
	}
	return Actor{ID: u.ID, Username: u.Username, Role: c.Role, Kind: c.Kind}, nil
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

func (a *Admin) authenticate(r *http.Request) (Actor, error) {
	raw := bearer(r)
	if raw == "" {
		return Actor{}, errUnauthorized
	}
	if strings.HasPrefix(raw, tokenPrefix) {
		t, err := a.db.GetTokenByHash(hashSecret(raw))
		if err != nil || t.Expired() {
			return Actor{}, errUnauthorized
		}
		u, err := a.db.GetUser(t.UserID)
		if err != nil || u.Disabled {
			return Actor{}, errUnauthorized
		}
		return Actor{ID: u.ID, Username: u.Username, Role: t.Role, Kind: "token"}, nil
	}
	return a.parseJWT(raw)
}

func (a *Admin) authRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := a.authenticate(r)
		if err != nil {
			authCount.WithLabelValues("unauth").Inc()
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		authCount.WithLabelValues("ok").Inc()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey, actor)))
	})
}

func actorFrom(r *http.Request) Actor {
	v, _ := r.Context().Value(actorKey).(Actor)
	return v
}

func requireRole(need string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := actorFrom(r)
		if err := store.RequireRole(actor.Role, need); err != nil {
			authCount.WithLabelValues("forbidden").Inc()
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

func (a *Admin) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Password {
		writeError(w, http.StatusForbidden, "password login disabled")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := a.db.GetUserByName(store.NormalizeUsername(body.Username))
	if err != nil || u.Disabled || !verifyPassword(u.PasswordHash, body.Password) {
		authCount.WithLabelValues("login_fail").Inc()
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	actor := Actor{ID: u.ID, Username: u.Username, Role: u.Role, Kind: "user"}
	tok, exp, err := a.issueJWT(actor, jwtTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": exp.Unix(), "user": actor})
}

func (a *Admin) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *Admin) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, actorFrom(r))
}

const defaultOIDCButton = "Continue with OIDC"

func (a *Admin) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	dbOIDC, dbErr := a.db.GetOIDC()
	oidcOn := dbErr == nil || a.cfg.OIDC != nil
	issuer := ""
	text := defaultOIDCButton
	image := ""
	if dbErr == nil {
		issuer = dbOIDC.Issuer
		if dbOIDC.ButtonText != "" {
			text = dbOIDC.ButtonText
		}
		image = dbOIDC.ButtonImage
	}
	if a.cfg.OIDC != nil {
		issuer = a.cfg.OIDC.Issuer
		if a.cfg.OIDC.ButtonText != "" {
			text = a.cfg.OIDC.ButtonText
		}
		if a.cfg.OIDC.ButtonImage != "" {
			image = a.cfg.OIDC.ButtonImage
		}
	}
	out := map[string]any{
		"password":    a.cfg.Password,
		"oidc":        oidcOn,
		"oidc_issuer": issuer,
	}
	if oidcOn {
		out["oidc_button_text"] = text
		if image != "" {
			out["oidc_button_image"] = image
		}
	}
	writeJSON(w, http.StatusOK, out)
}
