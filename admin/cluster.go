package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skymoore/coredns-ez/admin/store"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

func (a *Admin) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	id, _ := a.db.Meta(store.MetaClusterID)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      id,
		"role":    a.cfg.Role,
		"self_id": a.selfMemberID(),
		"members": a.roster(r),
	})
}

func (a *Admin) handleCreateJoinToken(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "only a primary issues join tokens")
		return
	}
	var body struct {
		TTL string `json:"ttl"`
	}
	_ = readJSON(r, &body)
	ttl := 24 * time.Hour
	if body.TTL != "" {
		d, err := time.ParseDuration(body.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid ttl")
			return
		}
		ttl = d
	}
	plain, hash, _, err := newSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token")
		return
	}
	jt, err := a.db.InsertJoinToken(hash, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	adv, _ := a.db.Meta(store.MetaAdvertise)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            jt.ID,
		"token":         plain,
		"expires_at":    jt.ExpiresAt,
		"primary_url":   requestBaseURL(r),
		"advertise_dns": adv,
	})
}

func (a *Admin) handleClusterConnect(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != roleSecondary {
		writeError(w, http.StatusForbidden, "connect is for secondaries")
		return
	}
	if id, _ := a.db.Meta(store.MetaClusterID); id != "" {
		writeError(w, http.StatusConflict, "already joined")
		return
	}
	var body struct {
		URL    string `json:"url"`
		Token  string `json:"token"`
		DNS    string `json:"dns"`
		APIURL string `json:"api_url"`
	}
	if err := readJSON(r, &body); err != nil || body.URL == "" || body.Token == "" {
		writeError(w, http.StatusBadRequest, "url and token required")
		return
	}
	name, _ := os.Hostname()
	apiURL := body.APIURL
	if apiURL == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		apiURL = scheme + "://" + r.Host
	}
	dnsAddr := body.DNS
	if dnsAddr == "" {
		dnsAddr = a.cfg.AdvertiseDNS
	}
	if err := a.joinPrimary(body.URL, body.Token, name, dnsAddr, apiURL); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}

func (a *Admin) handleClusterJoin(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "join on the primary")
		return
	}
	var body struct {
		Token   string `json:"token"`
		Name    string `json:"name"`
		APIURL  string `json:"api_url"`
		DNSAddr string `json:"dns_addr"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := a.db.ConsumeJoinToken(hashSecret(body.Token)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid join token")
		return
	}
	plain, hash, _, err := newSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "secret")
		return
	}
	if err := a.ensureSelfMember(requestBaseURL(r)); err != nil {
		log.Warningf("cluster self member: %v", err)
	}
	m := store.Member{Name: body.Name, APIURL: body.APIURL, DNSAddr: body.DNSAddr, SecretHash: hash, Role: store.MemberSecondary}
	inserted, err := a.db.InsertMember(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.appendTransferAddr(body.DNSAddr)
	_, _ = a.db.BumpGeneration()
	clusterID, _ := a.db.Meta(store.MetaClusterID)
	if clusterID == "" {
		clusterID, _ = randomHex(8)
		_ = a.db.SetMeta(store.MetaClusterID, clusterID)
	}
	nodeID, _ := a.db.Meta(store.MetaNodeID)
	if clusterID == "" {
		clusterID = nodeID
		_ = a.db.SetMeta(store.MetaClusterID, clusterID)
	}
	snap, err := a.db.Snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	adv, _ := a.db.Meta(store.MetaAdvertise)
	writeJSON(w, http.StatusCreated, map[string]any{
		"cluster_id":    clusterID,
		"member_id":     inserted.ID,
		"member_secret": plain,
		"advertise_dns": adv,
		"snapshot":      snap,
	})
}

func (a *Admin) handleListMembers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"members": a.roster(r)})
}

func (a *Admin) handleDeleteMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := a.db.GetMember(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if memberRole(m) == store.MemberPrimary {
		writeError(w, http.StatusConflict, "cannot remove the primary")
		return
	}
	if err := a.db.DeleteMember(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = a.db.BumpGeneration()
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) memberAuth(r *http.Request) (store.Member, bool) {
	raw := bearer(r)
	if raw == "" {
		return store.Member{}, false
	}
	m, err := a.db.GetMemberBySecretHash(hashSecret(raw))
	return m, err == nil
}

func (a *Admin) handleClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "primary only")
		return
	}
	m, ok := a.memberAuth(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	_ = a.db.TouchMember(m.ID)
	snap, err := a.db.Snapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (a *Admin) handleClusterSnapshotApply(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != roleSecondary {
		writeError(w, http.StatusForbidden, "secondary only")
		return
	}
	sec, _ := a.db.Meta(store.MetaMemberSec)
	if bearer(r) != sec {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var snap store.Snapshot
	if err := readJSON(r, &snap); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := a.applySnapshot(snap); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"generation": snap.Generation})
}

func (a *Admin) applySnapshot(snap store.Snapshot) error {
	if err := a.db.ApplySnapshot(snap); err != nil {
		return err
	}
	for _, z := range snap.Zones {
		if z.Kind != zonereg.KindPrimary {
			continue
		}
		if zonereg.SecondaryOf(z.Origin) != nil {
			continue
		}
		from := []string{a.cfg.PrimaryDNS}
		if a.cfg.PrimaryDNS == "" {
			adv, _ := a.db.Meta(store.MetaAdvertise)
			from = []string{adv}
		}
		if err := a.createSecondaryNoPersist(z.Origin, from); err != nil {
			log.Warningf("sync zone %s: %v", z.Origin, err)
		}
	}
	a.publishTSIG()
	a.publishFilter()
	a.publishTransfer()
	syncCount.WithLabelValues("ok").Inc()
	a.refreshZoneMetrics()
	return nil
}

func (a *Admin) pullLoop() {
	if err := a.pullSnapshot(); err != nil {
		syncCount.WithLabelValues("error").Inc()
		log.Warningf("cluster pull: %v", err)
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			if err := a.pullSnapshot(); err != nil {
				syncCount.WithLabelValues("error").Inc()
				log.Warningf("cluster pull: %v", err)
			}
		}
	}
}

func (a *Admin) pullSnapshot() error {
	url, _ := a.db.Meta(store.MetaPrimaryURL)
	sec, _ := a.db.Meta(store.MetaMemberSec)
	if url == "" || sec == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, url+"/api/v1/cluster/snapshot", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+sec)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("snapshot status %d: %s", resp.StatusCode, body)
	}
	var snap store.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return err
	}
	if snap.Generation <= a.db.Generation() {
		return nil
	}
	return a.applySnapshot(snap)
}

func (a *Admin) pushSnapshot() {
	// Primary does not retain member secrets, so it cannot push. Secondaries
	// pull /cluster/snapshot every 30s.
}

func (a *Admin) joinPrimary(url, token, name, dnsAddr, apiURL string) error {
	payload, _ := json.Marshal(map[string]string{
		"token": token, "name": name, "api_url": apiURL, "dns_addr": dnsAddr,
	})
	resp, err := a.httpClient.Post(url+"/api/v1/cluster/join", "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("join status %d: %s", resp.StatusCode, b)
	}
	var out struct {
		ClusterID    string         `json:"cluster_id"`
		MemberID     string         `json:"member_id"`
		MemberSecret string         `json:"member_secret"`
		AdvertiseDNS string         `json:"advertise_dns"`
		Snapshot     store.Snapshot `json:"snapshot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	_ = a.db.SetMeta(store.MetaClusterID, out.ClusterID)
	_ = a.db.SetMeta(store.MetaMemberSec, out.MemberSecret)
	_ = a.db.SetMeta(store.MetaPrimaryURL, url)
	if out.MemberID != "" {
		_ = a.db.SetMeta(store.MetaMemberID, out.MemberID)
	}
	if out.AdvertiseDNS != "" {
		_ = a.db.SetMeta(store.MetaAdvertise, out.AdvertiseDNS)
		if a.cfg.PrimaryDNS == "" {
			a.cfg.PrimaryDNS = out.AdvertiseDNS
		}
	}
	return a.applySnapshot(out.Snapshot)
}
