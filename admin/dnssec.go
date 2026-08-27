package admin

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coredns/coredns/plugin/pkg/response"
	"github.com/coredns/coredns/request"
	"github.com/go-chi/chi/v5"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	"github.com/skymoore/coredns-ez/internal/zonereg"
	"golang.org/x/crypto/ed25519"
)

const dnssecAlg = dns.ECDSAP256SHA256

type loadedCSK struct {
	origin string
	pub    *dns.DNSKEY
	signer crypto.Signer
	tag    uint16
}

func (a *Admin) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return a.serveWithNext(ctx, w, r, a.Next)
}

// wrapDNSSEC answers apex DNSKEY from sqlite and signs DO responses. This has
// to live on serveWithNext: the Corefile path is adminChain, which never calls
// ServeDNS.
func (a *Admin) wrapDNSSEC(w dns.ResponseWriter, r *dns.Msg) (dns.ResponseWriter, bool, int, error) {
	origin := a.dnssecZone(r)
	if origin == "" {
		return w, false, 0, nil
	}
	if r != nil && len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeDNSKEY &&
		strings.EqualFold(dns.CanonicalName(r.Question[0].Name), origin) {
		code, err := a.serveDNSKEY(w, r, origin)
		return w, true, code, err
	}
	st := request.Request{W: w, Req: r}
	if st.Do() {
		w = &signWriter{ResponseWriter: w, a: a, origin: origin}
	}
	return w, false, 0, nil
}

func (a *Admin) dnssecZone(r *dns.Msg) string {
	if r == nil || len(r.Question) == 0 {
		return ""
	}
	qname := strings.ToLower(dns.CanonicalName(r.Question[0].Name))
	a.mu.RLock()
	defer a.mu.RUnlock()
	var best string
	for origin := range a.dnssecKeys {
		if qname == origin || dns.IsSubDomain(origin, qname) {
			if len(origin) > len(best) {
				best = origin
			}
		}
	}
	return best
}

func (a *Admin) keysFor(origin string) []loadedCSK {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.dnssecKeys[origin]
}

func (a *Admin) rebuildSigner() {
	rows, err := a.db.ListDNSSECKeys()
	if err != nil {
		log.Warningf("dnssec keys: %v", err)
		return
	}
	next := map[string][]loadedCSK{}
	for _, row := range rows {
		k, err := loadCSK(row)
		if err != nil {
			log.Warningf("dnssec parse %s: %v", row.Origin, err)
			continue
		}
		next[k.origin] = append(next[k.origin], *k)
	}
	a.mu.Lock()
	a.dnssecKeys = next
	a.mu.Unlock()
}

func loadCSK(row store.DNSSECKey) (*loadedCSK, error) {
	rr, err := dns.NewRR(row.Public)
	if err != nil {
		return nil, err
	}
	dk, ok := rr.(*dns.DNSKEY)
	if !ok {
		return nil, fmt.Errorf("public key is not a DNSKEY")
	}
	p, err := dk.ReadPrivateKey(strings.NewReader(row.Private), row.Origin)
	if err != nil {
		return nil, err
	}
	var s crypto.Signer
	switch t := p.(type) {
	case *ecdsa.PrivateKey:
		s = t
	case ed25519.PrivateKey:
		s = t
	default:
		return nil, fmt.Errorf("unsupported dnssec private key")
	}
	return &loadedCSK{origin: row.Origin, pub: dk, signer: s, tag: dk.KeyTag()}, nil
}

func generateCSK(origin string) (store.DNSSECKey, error) {
	origin = strings.ToLower(dns.CanonicalName(origin))
	k := &dns.DNSKEY{
		Hdr:       dns.RR_Header{Name: origin, Rrtype: dns.TypeDNSKEY, Class: dns.ClassINET, Ttl: 3600},
		Flags:     257,
		Protocol:  3,
		Algorithm: dnssecAlg,
	}
	priv, err := k.Generate(256)
	if err != nil {
		return store.DNSSECKey{}, err
	}
	return store.DNSSECKey{
		Origin:    origin,
		KeyTag:    int(k.KeyTag()),
		Algorithm: int(k.Algorithm),
		Flags:     int(k.Flags),
		Public:    k.String(),
		Private:   k.PrivateKeyString(priv),
	}, nil
}

func dnssecInfoJSON(k store.DNSSECKey) map[string]any {
	algName := dns.AlgorithmToString[uint8(k.Algorithm)]
	out := map[string]any{
		"enabled":       true,
		"algorithm":     algName,
		"key_tag":       k.KeyTag,
		"flags":         k.Flags,
		"protocol":      3,
		"dnskey":        strings.TrimSpace(k.Public),
		"max_sig_life":  int((8 * 24 * time.Hour).Seconds()),
	}
	rr, err := dns.NewRR(k.Public)
	if err != nil {
		return out
	}
	dk, ok := rr.(*dns.DNSKEY)
	if !ok {
		return out
	}
	out["key_data"] = map[string]any{
		"flags":           int(dk.Flags),
		"protocol":        int(dk.Protocol),
		"algorithm":       int(dk.Algorithm),
		"algorithm_name":  algName,
		"public_key":      dk.PublicKey,
	}
	if ds := dk.ToDS(dns.SHA256); ds != nil {
		out["ds"] = strings.TrimSpace(ds.String())
		out["ds_digest"] = ds.Digest
		out["ds_data"] = map[string]any{
			"key_tag":          int(ds.KeyTag),
			"algorithm":        int(ds.Algorithm),
			"algorithm_name":   algName,
			"digest_type":      int(ds.DigestType),
			"digest_type_name": "SHA-256",
			"digest":           ds.Digest,
		}
		if cds := ds.ToCDS(); cds != nil {
			out["cds"] = strings.TrimSpace(cds.String())
		}
	}
	if cd := dk.ToCDNSKEY(); cd != nil {
		out["cdnskey"] = strings.TrimSpace(cd.String())
	}
	return out
}

func (a *Admin) serveDNSKEY(w dns.ResponseWriter, r *dns.Msg, origin string) (int, error) {
	keys := a.keysFor(origin)
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	var pubs []dns.RR
	for _, k := range keys {
		pubs = append(pubs, dns.Copy(k.pub))
	}
	m.Answer = pubs
	st := request.Request{W: w, Req: r}
	if st.Do() {
		a.signMsg(m, origin)
	}
	if err := w.WriteMsg(m); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (a *Admin) answerDNSSECMeta(w dns.ResponseWriter, r *dns.Msg) bool {
	if r == nil || len(r.Question) == 0 {
		return false
	}
	q := r.Question[0]
	if q.Qtype != dns.TypeCDS && q.Qtype != dns.TypeCDNSKEY {
		return false
	}
	origin := strings.ToLower(dns.CanonicalName(q.Name))
	keys, err := a.db.GetDNSSECKeys(origin)
	if err != nil || len(keys) == 0 {
		return false
	}
	info := dnssecInfoJSON(keys[0])
	var rr dns.RR
	var parseErr error
	if q.Qtype == dns.TypeCDS {
		s, _ := info["cds"].(string)
		if s == "" {
			return false
		}
		rr, parseErr = dns.NewRR(s)
	} else {
		s, _ := info["cdnskey"].(string)
		if s == "" {
			return false
		}
		rr, parseErr = dns.NewRR(s)
	}
	if parseErr != nil || rr == nil {
		return false
	}
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.Answer = []dns.RR{rr}
	_ = w.WriteMsg(m)
	return true
}

type signWriter struct {
	dns.ResponseWriter
	a      *Admin
	origin string
}

func (s *signWriter) WriteMsg(res *dns.Msg) error {
	if res != nil {
		s.a.signMsg(res, s.origin)
	}
	return s.ResponseWriter.WriteMsg(res)
}

func (a *Admin) signMsg(m *dns.Msg, origin string) {
	keys := a.keysFor(origin)
	if len(keys) == 0 || m == nil {
		return
	}
	now := time.Now().UTC()
	incep := uint32(now.Add(-3 * time.Hour).Unix())
	expir := uint32(now.Add(8 * 24 * time.Hour).Unix())
	state := request.Request{W: nil, Req: m}
	state.Zone = origin
	mt, _ := response.Typify(m, now)
	if mt == response.NameError || mt == response.NoData {
		if nsec := blackLieNSEC(state, origin, mt); nsec != nil {
			m.Ns = append(m.Ns, nsec)
			m.Rcode = dns.RcodeSuccess
		}
	}
	signSection := func(rrs []dns.RR) []dns.RR {
		out := rrs
		for _, set := range rrSets(rrs) {
			if sigs := signRRset(keys, origin, set, incep, expir); len(sigs) > 0 {
				out = append(out, sigs...)
			}
		}
		return out
	}
	m.Answer = signSection(m.Answer)
	m.Ns = signSection(m.Ns)
	m.Extra = signSection(m.Extra)
}

func signRRset(keys []loadedCSK, origin string, rrs []dns.RR, incep, expir uint32) []dns.RR {
	if len(rrs) == 0 {
		return nil
	}
	ttl := rrs[0].Header().Ttl
	var sigs []dns.RR
	for _, k := range keys {
		sig := &dns.RRSIG{
			Hdr: dns.RR_Header{
				Name:   rrs[0].Header().Name,
				Rrtype: dns.TypeRRSIG,
				Class:  dns.ClassINET,
				Ttl:    ttl,
			},
			TypeCovered: rrs[0].Header().Rrtype,
			Algorithm:   k.pub.Algorithm,
			Labels:      uint8(dns.CountLabel(rrs[0].Header().Name)),
			OrigTtl:     ttl,
			Expiration:  expir,
			Inception:   incep,
			KeyTag:      k.tag,
			SignerName:  origin,
		}
		if err := sig.Sign(k.signer, rrs); err != nil {
			log.Warningf("dnssec sign %s: %v", origin, err)
			continue
		}
		sigs = append(sigs, sig)
	}
	return sigs
}

func rrSets(rrs []dns.RR) [][]dns.RR {
	type key struct {
		name string
		t    uint16
	}
	m := map[key][]dns.RR{}
	var order []key
	for _, r := range rrs {
		if r == nil {
			continue
		}
		h := r.Header()
		if h.Rrtype == dns.TypeRRSIG || h.Rrtype == dns.TypeOPT {
			continue
		}
		k := key{h.Name, h.Rrtype}
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = append(m[k], r)
	}
	out := make([][]dns.RR, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	return out
}

func blackLieNSEC(state request.Request, origin string, mt response.Type) *dns.NSEC {
	qname := state.QName()
	nsec := &dns.NSEC{
		Hdr:        dns.RR_Header{Name: qname, Rrtype: dns.TypeNSEC, Class: dns.ClassINET, Ttl: 300},
		NextDomain: `\000.` + qname,
		TypeBitMap: []uint16{dns.TypeA, dns.TypeNS, dns.TypeSOA, dns.TypeMX, dns.TypeTXT, dns.TypeAAAA, dns.TypeRRSIG, dns.TypeNSEC, dns.TypeDNSKEY},
	}
	if qname == origin {
		nsec.TypeBitMap = append(nsec.TypeBitMap, dns.TypeCDS, dns.TypeCDNSKEY)
	}
	_ = mt
	return nsec
}

func (a *Admin) handleGetDNSSEC(w http.ResponseWriter, r *http.Request) {
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	keys, err := a.db.GetDNSSECKeys(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(keys) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, dnssecInfoJSON(keys[0]))
}

func (a *Admin) handleEnableDNSSEC(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "enable DNSSEC on the primary")
		return
	}
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	if _, err := a.db.GetZone(origin); err != nil && zonereg.PrimaryOf(origin) == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	existing, err := a.db.GetDNSSECKeys(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(existing) > 0 {
		writeJSON(w, http.StatusOK, dnssecInfoJSON(existing[0]))
		return
	}
	k, err := generateCSK(origin)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := a.db.InsertDNSSECKey(k); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.rebuildSigner()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "dnssec.enable", origin, "")
	writeJSON(w, http.StatusCreated, dnssecInfoJSON(k))
}

func (a *Admin) handleDisableDNSSEC(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "disable DNSSEC on the primary")
		return
	}
	origin := strings.ToLower(dns.CanonicalName(chi.URLParam(r, "origin")))
	if err := a.db.DeleteDNSSECKeys(origin); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.rebuildSigner()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "dnssec.disable", origin, "")
	w.WriteHeader(http.StatusNoContent)
}
