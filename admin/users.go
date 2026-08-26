package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/skymoore/coredns-plugins/admin/store"
)

func (a *Admin) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := a.db.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range users {
		users[i].PasswordHash = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *Admin) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	body.Username = store.NormalizeUsername(body.Username)
	if body.Username == "" || body.Password == "" || !store.ValidRole(body.Role) {
		writeError(w, http.StatusBadRequest, "username, password, and role required")
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash")
		return
	}
	u := store.User{Username: body.Username, PasswordHash: hash, Role: body.Role}
	if err := a.db.CreateUser(u); err != nil {
		writeError(w, http.StatusConflict, "username taken")
		return
	}
	created, _ := a.db.GetUserByName(body.Username)
	a.db.Audit(actorFrom(r).Username, "user.create", "", created.Username)
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	created.PasswordHash = ""
	writeJSON(w, http.StatusCreated, created)
}

func (a *Admin) handleGetUser(w http.ResponseWriter, r *http.Request) {
	u, err := a.db.GetUser(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	u.PasswordHash = ""
	writeJSON(w, http.StatusOK, u)
}

func (a *Admin) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	u, err := a.db.GetUser(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Password != nil {
		hash, err := hashPassword(*body.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "hash")
			return
		}
		u.PasswordHash = hash
	}
	if body.Role != nil {
		if !store.ValidRole(*body.Role) {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		u.Role = *body.Role
	}
	if body.Disabled != nil {
		u.Disabled = *body.Disabled
	}
	if err := a.db.UpdateUser(u); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	u.PasswordHash = ""
	writeJSON(w, http.StatusOK, u)
}

func (a *Admin) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u, err := a.db.GetUser(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if u.Role == store.RoleAdmin {
		n, _ := a.db.AdminCount()
		if n <= 1 {
			writeError(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
	}
	if err := a.db.DeleteUser(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.db.Audit(actorFrom(r).Username, "user.delete", "", u.Username)
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	w.WriteHeader(http.StatusNoContent)
}
