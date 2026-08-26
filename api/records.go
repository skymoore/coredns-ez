package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-plugins/internal/zonereg"
)

type recordJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	Rdata string `json:"rdata"`
}

func rrToJSON(rr dns.RR) recordJSON {
	h := rr.Header()
	return recordJSON{
		Name:  h.Name,
		Type:  dns.TypeToString[h.Rrtype],
		TTL:   h.Ttl,
		Rdata: strings.TrimSpace(strings.TrimPrefix(rr.String(), rr.Header().String())),
	}
}

func parseRecord(j recordJSON) (dns.RR, error) {
	if j.Name == "" || j.Type == "" || j.Rdata == "" {
		return nil, fmt.Errorf("name, type, and rdata required")
	}
	line := fmt.Sprintf("%s %d IN %s %s", j.Name, j.TTL, j.Type, j.Rdata)
	if j.TTL == 0 {
		line = fmt.Sprintf("%s IN %s %s", j.Name, j.Type, j.Rdata)
	}
	return dns.NewRR(line)
}

func (a *API) handleListRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	var rrs []dns.RR
	if p := zonereg.PrimaryOf(origin); p != nil {
		rrs = p.Records()
	} else if s := zonereg.SecondaryOf(origin); s != nil {
		rrs = s.Records()
	} else {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	wantName := r.URL.Query().Get("name")
	wantType := strings.ToUpper(r.URL.Query().Get("type"))
	out := []recordJSON{}
	for _, rr := range rrs {
		j := rrToJSON(rr)
		if wantName != "" && !strings.EqualFold(j.Name, dns.CanonicalName(wantName)) {
			continue
		}
		if wantType != "" && j.Type != wantType {
			continue
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": out})
}

func (a *API) handleAddRecord(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	p := zonereg.PrimaryOf(origin)
	if p == nil {
		writeError(w, http.StatusForbidden, "not a writable primary")
		return
	}
	var body recordJSON
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	rr, err := parseRecord(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := p.Apply([]dns.RR{rr}, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.db.Audit(actorFrom(r).Username, "record.add", origin, rr.String())
	writeJSON(w, http.StatusCreated, rrToJSON(rr))
}

func (a *API) handleReplaceRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	p := zonereg.PrimaryOf(origin)
	if p == nil {
		writeError(w, http.StatusForbidden, "not a writable primary")
		return
	}
	var body struct {
		Name    string       `json:"name"`
		Type    string       `json:"type"`
		Records []recordJSON `json:"records"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Name == "" || body.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type required")
		return
	}
	typ, ok := dns.StringToType[strings.ToUpper(body.Type)]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown type")
		return
	}
	del := &dns.ANY{Hdr: dns.RR_Header{Name: dns.CanonicalName(body.Name), Rrtype: typ, Class: dns.ClassANY, Ttl: 0}}
	var adds []dns.RR
	for _, j := range body.Records {
		rr, err := parseRecord(j)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		adds = append(adds, rr)
	}
	if err := p.Apply(adds, []dns.RR{del}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "replaced"})
}

func (a *API) handleDeleteRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	p := zonereg.PrimaryOf(origin)
	if p == nil {
		writeError(w, http.StatusForbidden, "not a writable primary")
		return
	}
	var body recordJSON
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	var del dns.RR
	if body.Rdata == "" {
		typ, ok := dns.StringToType[strings.ToUpper(body.Type)]
		if !ok || body.Name == "" {
			writeError(w, http.StatusBadRequest, "name and type required")
			return
		}
		del = &dns.ANY{Hdr: dns.RR_Header{Name: dns.CanonicalName(body.Name), Rrtype: typ, Class: dns.ClassANY, Ttl: 0}}
	} else {
		rr, err := parseRecord(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rr.Header().Class = dns.ClassNONE
		del = rr
	}
	if err := p.Apply(nil, []dns.RR{del}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
