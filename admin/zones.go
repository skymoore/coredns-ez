package admin

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

type zoneJSON struct {
	Origin       string   `json:"origin"`
	Kind         string   `json:"kind"`
	Source       string   `json:"source"`
	Path         string   `json:"path,omitempty"`
	TransferFrom []string `json:"transfer_from,omitempty"`
	Mutable      []string `json:"mutable,omitempty"`
	Serial       uint32   `json:"serial,omitempty"`
	Dnssec       bool     `json:"dnssec"`
}

func (a *Admin) handleListZones(w http.ResponseWriter, _ *http.Request) {
	seen := map[string]bool{}
	signed := a.db.DNSSECOriginSet()
	var out []zoneJSON
	rows, _ := a.db.ListZones()
	for _, row := range rows {
		z := zoneFromRow(row)
		z.Dnssec = signed[z.Origin]
		seen[z.Origin] = true
		out = append(out, z)
	}
	for _, info := range zonereg.All() {
		if seen[info.Origin] {
			continue
		}
		z := zoneJSON{Origin: info.Origin, Kind: info.Kind, Source: info.Source, Path: info.Path}
		fillZoneRuntime(&z)
		z.Dnssec = signed[z.Origin]
		out = append(out, z)
	}
	writeJSON(w, http.StatusOK, map[string]any{"zones": out})
}

func zoneFromRow(row store.ZoneRow) zoneJSON {
	z := zoneJSON{
		Origin:       row.Origin,
		Kind:         row.Kind,
		Source:       row.Source,
		Mutable:      store.SplitCSV(row.Mutable),
		TransferFrom: store.SplitCSV(row.TransferFrom),
	}
	fillZoneRuntime(&z)
	return z
}

func fillZoneRuntime(z *zoneJSON) {
	if p := zonereg.PrimaryOf(z.Origin); p != nil {
		z.Kind = zonereg.KindPrimary
		z.Source = p.Source()
		z.Path = p.Path()
		if soa := soaOf(p.Records()); soa != nil {
			z.Serial = soa.Serial
		}
		return
	}
	if s := zonereg.SecondaryOf(z.Origin); s != nil {
		z.Kind = zonereg.KindSecondary
		z.Source = s.Source()
		z.Path = s.Path()
		z.TransferFrom = s.TransferFrom()
		if soa := soaOf(s.Records()); soa != nil {
			z.Serial = soa.Serial
		}
	}
}

func (a *Admin) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "secondaries receive zones via cluster sync")
		return
	}
	var body struct {
		Origin       string   `json:"origin"`
		Type         string   `json:"type"`
		NS           string   `json:"ns"`
		Rname        string   `json:"rname"`
		TransferFrom []string `json:"transfer_from"`
		Mutable      []string `json:"mutable"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Type == "" {
		body.Type = zonereg.KindPrimary
	}
	var err error
	switch body.Type {
	case zonereg.KindPrimary:
		err = a.createPrimary(body.Origin, body.NS, body.Rname, body.Mutable)
	case zonereg.KindSecondary:
		err = a.createSecondary(body.Origin, body.TransferFrom)
	default:
		writeError(w, http.StatusBadRequest, "type must be primary or secondary")
		return
	}
	if err == errExists {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.db.Audit(actorFrom(r).Username, "zone.create", strings.ToLower(dns.CanonicalName(body.Origin)), body.Type)
	a.handleGetZoneOrigin(w, strings.ToLower(dns.CanonicalName(body.Origin)))
}

func (a *Admin) handleGetZone(w http.ResponseWriter, r *http.Request) {
	a.handleGetZoneOrigin(w, chi.URLParam(r, "origin"))
}

func (a *Admin) handleGetZoneOrigin(w http.ResponseWriter, origin string) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	var found zonereg.Info
	ok := false
	for _, info := range zonereg.All() {
		if info.Origin == origin {
			found, ok = info, true
			break
		}
	}
	if !ok {
		if row, err := a.db.GetZone(origin); err == nil {
			z := zoneFromRow(row)
			z.Dnssec = a.db.DNSSECOriginSet()[origin]
			writeJSON(w, http.StatusOK, z)
			return
		}
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	z := zoneJSON{Origin: found.Origin, Kind: found.Kind, Source: found.Source, Path: found.Path}
	if p := zonereg.PrimaryOf(origin); p != nil {
		if soa := soaOf(p.Records()); soa != nil {
			z.Serial = soa.Serial
		}
	}
	if s := zonereg.SecondaryOf(origin); s != nil {
		z.TransferFrom = s.TransferFrom()
		if soa := soaOf(s.Records()); soa != nil {
			z.Serial = soa.Serial
		}
	}
	z.Dnssec = a.db.DNSSECOriginSet()[origin]
	writeJSON(w, http.StatusOK, z)
}

func (a *Admin) handlePatchZone(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "patch zone: transfer ACL via cluster join")
}

func (a *Admin) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	info := zonereg.PrimaryOf(origin)
	sec := zonereg.SecondaryOf(origin)
	if info == nil && sec == nil {
		if _, err := a.db.GetZone(origin); err != nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
	} else {
		src := ""
		if info != nil {
			src = info.Source()
		} else {
			src = sec.Source()
		}
		if src != zonereg.SourceAdmin {
			writeError(w, http.StatusConflict, "corefile zones cannot be deleted via the admin plugin")
			return
		}
	}
	if err := a.deleteZone(origin); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) handleNotifyZone(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	if a.xfer == nil {
		writeError(w, http.StatusBadRequest, "no transfer plugin")
		return
	}
	if err := a.xfer.Notify(origin); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "notified"})
}

func (a *Admin) handleTransferZone(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	s := zonereg.SecondaryOf(origin)
	if s == nil {
		writeError(w, http.StatusNotFound, "not a secondary")
		return
	}
	if err := s.ForceTransfer(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "transferred"})
}

func soaOf(rrs []dns.RR) *dns.SOA {
	for _, rr := range rrs {
		if soa, ok := rr.(*dns.SOA); ok {
			return soa
		}
	}
	return nil
}
