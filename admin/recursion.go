package admin

import (
	"net/http"
	"strings"

	"github.com/skymoore/coredns-ez/admin/store"
)

func (a *Admin) handleGetRecursion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"networks": a.db.Recursion()})
}

func (a *Admin) handlePutRecursion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Networks []string `json:"networks"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	nets, err := store.NormalizeCIDRs(body.Networks)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.db.SetRecursion(nets); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "recursion.update", "", strings.Join(nets, ","))
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets})
}
