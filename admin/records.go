package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

type recordJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	Rdata string `json:"rdata"`
	ACL   string `json:"acl,omitempty"`
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

func parseRecordInZone(j recordJSON, origin string) (dns.RR, error) {
	if j.Type == "" || j.Rdata == "" {
		return nil, fmt.Errorf("type and rdata required")
	}
	name := j.Name
	if origin != "" {
		q, err := qualifyName(j.Name, origin)
		if err != nil {
			return nil, err
		}
		name = q
	} else if name == "" {
		return nil, fmt.Errorf("name, type, and rdata required")
	}
	line := fmt.Sprintf("%s %d IN %s %s", name, j.TTL, j.Type, j.Rdata)
	if j.TTL == 0 {
		line = fmt.Sprintf("%s IN %s %s", name, j.Type, j.Rdata)
	}
	return dns.NewRR(line)
}

// qualifyName expands BIND relative owners: blank/@ is the apex, "www" is
// www.<origin>. A trailing-dot name must already be this origin or a child.
func qualifyName(name, origin string) (string, error) {
	origin = dns.CanonicalName(origin)
	name = strings.TrimSpace(name)
	if name == "" || name == "@" {
		return origin, nil
	}
	lower := strings.ToLower(strings.TrimSuffix(name, "."))
	originBare := strings.TrimSuffix(origin, ".")
	if lower == originBare || strings.HasSuffix(lower, "."+originBare) {
		return dns.CanonicalName(name), nil
	}
	if strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("name is outside zone %s", origin)
	}
	return dns.CanonicalName(name + "." + originBare), nil
}

func (a *Admin) collectRecords(origin, wantACL string) ([]recordJSON, bool) {
	add := func(rrs []dns.RR, acl string, out *[]recordJSON) {
		for _, rr := range rrs {
			j := rrToJSON(rr)
			j.ACL = acl
			*out = append(*out, j)
		}
	}
	var out []recordJSON
	if wantACL == "" || wantACL == "public" {
		if p := zonereg.PrimaryOf(origin); p != nil {
			add(p.Records(), "", &out)
		} else if s := zonereg.SecondaryOf(origin); s != nil {
			add(s.Records(), "", &out)
		} else {
			return nil, false
		}
		if wantACL == "public" {
			return out, true
		}
	}
	a.mu.RLock()
	views := a.views[origin]
	a.mu.RUnlock()
	for acl, v := range views {
		if wantACL != "" && wantACL != "public" && acl != wantACL {
			continue
		}
		if wantACL == "public" {
			continue
		}
		add(v.Records(), acl, &out)
	}
	if len(out) == 0 && zonereg.PrimaryOf(origin) == nil && zonereg.SecondaryOf(origin) == nil {
		return nil, false
	}
	return out, true
}

func (a *Admin) handleListRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	wantACL := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("acl")))
	recs, ok := a.collectRecords(origin, wantACL)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	wantName := r.URL.Query().Get("name")
	if wantName != "" {
		if q, err := qualifyName(wantName, origin); err == nil {
			wantName = q
		} else {
			wantName = dns.CanonicalName(wantName)
		}
	}
	wantType := strings.ToUpper(r.URL.Query().Get("type"))
	out := []recordJSON{}
	for _, j := range recs {
		if wantName != "" && !strings.EqualFold(j.Name, wantName) {
			continue
		}
		if wantType != "" && j.Type != wantType {
			continue
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": out})
}

func (a *Admin) handleAddRecord(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	var body recordJSON
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := a.recordZone(origin, body.ACL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rr, err := parseRecordInZone(body, origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := p.Apply([]dns.RR{rr}, nil); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := rrToJSON(rr)
	out.ACL = strings.ToLower(strings.TrimSpace(body.ACL))
	a.db.Audit(actorFrom(r).Username, "record.add", origin, rr.String())
	writeJSON(w, http.StatusCreated, out)
}

func (a *Admin) handlePatchRecord(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	var body struct {
		Old recordJSON `json:"old"`
		New recordJSON `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldACL := body.Old.ACL
	newACL := body.New.ACL
	if newACL == "" {
		newACL = oldACL
	}
	oldP, err := a.recordZone(origin, oldACL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	oldRR, err := parseRecordInZone(body.Old, origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	newRR, err := parseRecordInZone(body.New, origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	oldRR.Header().Class = dns.ClassNONE
	if strings.ToLower(strings.TrimSpace(oldACL)) == strings.ToLower(strings.TrimSpace(newACL)) {
		if err := oldP.Apply([]dns.RR{newRR}, []dns.RR{oldRR}); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		if err := oldP.Apply(nil, []dns.RR{oldRR}); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		newP, err := a.recordZone(origin, newACL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := newP.Apply([]dns.RR{newRR}, nil); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	out := rrToJSON(newRR)
	out.ACL = strings.ToLower(strings.TrimSpace(newACL))
	a.db.Audit(actorFrom(r).Username, "record.update", origin, oldRR.String()+" -> "+newRR.String())
	writeJSON(w, http.StatusOK, out)
}

func (a *Admin) handleReplaceRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	var body struct {
		Name    string       `json:"name"`
		Type    string       `json:"type"`
		ACL     string       `json:"acl"`
		Records []recordJSON `json:"records"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := a.recordZone(origin, body.ACL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Type == "" {
		writeError(w, http.StatusBadRequest, "name and type required")
		return
	}
	qname, err := qualifyName(body.Name, origin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	typ, ok := dns.StringToType[strings.ToUpper(body.Type)]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown type")
		return
	}
	del := &dns.ANY{Hdr: dns.RR_Header{Name: qname, Rrtype: typ, Class: dns.ClassANY, Ttl: 0}}
	var adds []dns.RR
	for _, j := range body.Records {
		rr, err := parseRecordInZone(j, origin)
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

func (a *Admin) handleDeleteRecords(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	var body recordJSON
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := a.recordZone(origin, body.ACL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var del dns.RR
	if body.Rdata == "" {
		typ, ok := dns.StringToType[strings.ToUpper(body.Type)]
		qname, qerr := qualifyName(body.Name, origin)
		if !ok || qerr != nil || qname == "" {
			writeError(w, http.StatusBadRequest, "name and type required")
			return
		}
		del = &dns.ANY{Hdr: dns.RR_Header{Name: qname, Rrtype: typ, Class: dns.ClassANY, Ttl: 0}}
	} else {
		rr, err := parseRecordInZone(body, origin)
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
