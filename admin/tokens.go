package admin

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skymoore/coredns-plugins/admin/store"
)

func (a *Admin) handleListTokens(w http.ResponseWriter, r *http.Request) {
	actor := actorFrom(r)
	userID := ""
	if actor.Role != store.RoleAdmin {
		userID = actor.ID
	}
	toks, err := a.db.ListTokens(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range toks {
		toks[i].TokenHash = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": toks})
}

func (a *Admin) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		TTL    string `json:"ttl"`
		UserID string `json:"user_id"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	actor := actorFrom(r)
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	role := body.Role
	if role == "" {
		role = actor.Role
	}
	if !store.ValidRole(role) {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	if store.RequireRole(actor.Role, role) != nil {
		writeError(w, http.StatusForbidden, "cannot mint a higher role")
		return
	}
	userID := actor.ID
	if body.UserID != "" {
		if actor.Role != store.RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		userID = body.UserID
	}
	plain, hash, prefix, err := newSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token")
		return
	}
	t := store.Token{UserID: userID, Name: body.Name, TokenHash: hash, Prefix: prefix, Role: role}
	if body.TTL != "" {
		d, err := time.ParseDuration(body.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ttl")
			return
		}
		exp := time.Now().Add(d).Unix()
		t.ExpiresAt = &exp
	}
	if err := a.db.CreateToken(t); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	created, _ := a.db.GetTokenByHash(hash)
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	writeJSON(w, http.StatusCreated, map[string]any{"token": created, "secret": plain})
}

func (a *Admin) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := a.db.GetToken(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	actor := actorFrom(r)
	if actor.Role != store.RoleAdmin && t.UserID != actor.ID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if err := a.db.DeleteToken(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	w.WriteHeader(http.StatusNoContent)
}
