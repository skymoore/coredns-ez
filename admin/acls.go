package admin

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/skymoore/coredns-ez/admin/store"
)

func (a *Admin) handleListACLs(w http.ResponseWriter, _ *http.Request) {
	acls, err := a.db.ListACLs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acls": acls})
}

func (a *Admin) handleCreateACL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string   `json:"name"`
		Networks []string `json:"networks"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	acl, err := a.db.InsertACL(store.ACL{Name: body.Name, Networks: body.Networks})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "acl.create", "", acl.Name)
	writeJSON(w, http.StatusCreated, acl)
}

func (a *Admin) handlePatchACL(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(chi.URLParam(r, "name"))
	var body struct {
		Name     string   `json:"name"`
		Networks []string `json:"networks"`
		Position *int     `json:"position"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	acl, err := a.db.UpdateACL(name, body.Name, body.Networks, body.Position)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if acl.Name != name {
		a.renameViews(name, acl.Name)
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "acl.update", "", acl.Name)
	writeJSON(w, http.StatusOK, acl)
}

func (a *Admin) renameViews(oldName, newName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for origin, m := range a.views {
		d, ok := m[oldName]
		if !ok || d == nil {
			continue
		}
		delete(m, oldName)
		d.SetPersist(a.persistView(origin, newName))
		m[newName] = d
		_ = a.db.UpsertZoneView(store.ZoneView{Origin: origin, ACL: newName})
	}
}

func (a *Admin) handleDeleteACL(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(chi.URLParam(r, "name"))
	gone, err := a.db.DeleteZoneViewsForACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, v := range gone {
		a.mu.Lock()
		if a.views[v.Origin] != nil {
			delete(a.views[v.Origin], name)
		}
		a.mu.Unlock()
	}
	if err := a.db.DeleteACL(name); err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	w.WriteHeader(http.StatusNoContent)
}
