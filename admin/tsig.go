package admin

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

func normalizeTSIGName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errBadTSIGName
	}
	name = strings.ToLower(dns.CanonicalName(name))
	if name == "." {
		return "", errBadTSIGName
	}
	if _, ok := dns.IsDomainName(name); !ok {
		return "", errBadTSIGName
	}
	return name, nil
}

func generateTSIGSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func (a *Admin) handleListTSIGKeys(w http.ResponseWriter, _ *http.Request) {
	keys, err := a.db.ListTSIGKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (a *Admin) handleCreateTSIGKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		Algorithm string `json:"algorithm"`
		Secret    string `json:"secret"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	name, err := normalizeTSIGName(body.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key name")
		return
	}
	alg, err := store.NormalizeTSIGAlg(body.Algorithm)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := strings.TrimSpace(body.Secret)
	if secret == "" {
		secret, err = generateTSIGSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "secret")
			return
		}
	} else if _, err := base64.StdEncoding.DecodeString(secret); err != nil {
		writeError(w, http.StatusBadRequest, "secret must be standard base64")
		return
	}
	k, err := a.db.CreateTSIGKey(store.TSIGKey{Name: name, Algorithm: alg, Secret: secret})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.publishTSIG()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "tsig.create", "", k.Name)
	writeJSON(w, http.StatusCreated, k)
}

func (a *Admin) handleDeleteTSIGKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	k, err := a.db.GetTSIGKey(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := a.db.DeleteTSIGKey(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.publishTSIG()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "tsig.delete", "", k.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) publishTSIG() {
	if a.tsig == nil {
		return
	}
	keys, err := a.db.ListTSIGKeys()
	if err != nil {
		log.Warningf("tsig load: %v", err)
		return
	}
	a.tsig.ReplaceDB(keys)
}
