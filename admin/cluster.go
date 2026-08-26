package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
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

func (a *Admin) hasSecondaries() bool {
	members, _ := a.db.ListMembers()
	for _, m := range members {
		if memberRole(m) == store.MemberSecondary {
			return true
		}
	}
	return false
}

func (a *Admin) alreadyJoined() bool {
	if sec, _ := a.db.Meta(store.MetaMemberSec); sec != "" {
		return true
	}
	if a.cfg.Role != roleSecondary {
		return false
	}
	id, _ := a.db.Meta(store.MetaClusterID)
	return id != ""
}

func (a *Admin) canJoinAsSecondary() bool {
	if a.alreadyJoined() {
		return false
	}
	if a.cfg.Role == roleSecondary {
		return true
	}
	if a.cfg.Role == rolePrimary {
		return !a.hasSecondaries()
	}
	return false
}

func (a *Admin) becomeSecondary() {
	a.cfg.Role = roleSecondary
	_ = a.db.SetMeta(store.MetaRole, roleSecondary)
	a.startPull()
}

func (a *Admin) startPull() {
	a.pullOnce.Do(func() { go a.pullLoop() })
}

func (a *Admin) handleClusterConnect(w http.ResponseWriter, r *http.Request) {
	if n, err := a.db.UserCount(); err == nil && n > 0 {
		actor, err := a.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := store.RequireRole(actor.Role, store.RoleAdmin); err != nil {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	if a.alreadyJoined() {
		writeError(w, http.StatusConflict, "already joined")
		return
	}
	if !a.canJoinAsSecondary() {
		writeError(w, http.StatusConflict, "this primary already has secondaries")
		return
	}
	var body struct {
		URL    string `json:"url"`
		Token  string `json:"token"`
		DNS    string `json:"dns"`
		Name   string `json:"name"`
		APIURL string `json:"api_url"`
	}
	if err := readJSON(r, &body); err != nil || body.URL == "" || body.Token == "" {
		writeError(w, http.StatusBadRequest, "url and token required")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = a.nodeName()
	}
	_ = a.db.SetMeta(store.MetaNodeName, name)
	dnsAddr := body.DNS
	if dnsAddr == "" {
		dnsAddr = a.cfg.AdvertiseDNS
	}
	apiURL := publicAPIURL(r, body.APIURL, dnsAddr)
	if err := a.joinPrimary(body.URL, body.Token, name, dnsAddr, apiURL); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.becomeSecondary()
	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// Do not restart here: the HTTP client (and admin UI) treat a dropped
	// connection as a failed join even after a successful snapshot apply.
	a.pendingReload = false
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
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		body.Name = "secondary"
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
	snap, err := a.fullSnapshot()
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

func (a *Admin) handlePatchMember(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Role != rolePrimary {
		writeError(w, http.StatusForbidden, "only the primary can rename members")
		return
	}
	id := chi.URLParam(r, "id")
	m, err := a.db.GetMember(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	m.Name = name
	if _, err := a.db.UpsertRosterMember(m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if memberRole(m) == store.MemberPrimary {
		_ = a.db.SetMeta(store.MetaNodeName, name)
	}
	_, _ = a.db.BumpGeneration()
	writeJSON(w, http.StatusOK, map[string]string{"id": m.ID, "name": name})
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
	snap, err := a.fullSnapshot()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (a *Admin) fullSnapshot() (store.Snapshot, error) {
	snap, err := a.db.Snapshot()
	if err != nil {
		return snap, err
	}
	pw := a.cfg.Password
	snap.Password = &pw
	seen := map[string]bool{}
	for _, z := range snap.Zones {
		seen[z.Origin] = true
	}
	for _, info := range zonereg.All() {
		if info.Kind != zonereg.KindPrimary || seen[info.Origin] {
			continue
		}
		seen[info.Origin] = true
		snap.Zones = append(snap.Zones, store.ZoneRow{
			Origin: info.Origin, Kind: zonereg.KindPrimary, Source: info.Source, PersistPath: info.Path,
		})
	}
	a.attachCorefile(&snap)
	return snap, nil
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
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if a.pendingReload {
		a.pendingReload = false
		a.scheduleCorefileReload()
	}
}

func (a *Admin) primaryTransferFrom() []string {
	if a.cfg.PrimaryDNS != "" {
		return []string{a.cfg.PrimaryDNS}
	}
	if adv, _ := a.db.Meta(store.MetaAdvertise); adv != "" {
		return []string{adv}
	}
	return nil
}

func (a *Admin) applySnapshot(snap store.Snapshot) error {
	if err := a.db.ApplySnapshot(snap); err != nil {
		return err
	}
	if snap.Password != nil {
		v := "off"
		if *snap.Password {
			v = "on"
		}
		_ = a.db.SetMeta(store.MetaPassword, v)
	}
	a.reloadOIDCFromDB()
	reload, err := a.applyCorefile(snap)
	if err != nil {
		log.Warningf("corefile sync: %v", err)
	}
	from := a.primaryTransferFrom()
	for _, z := range snap.Zones {
		if z.Kind != zonereg.KindPrimary {
			continue
		}
		if zonereg.SecondaryOf(z.Origin) != nil {
			continue
		}
		if len(from) == 0 {
			log.Warningf("sync zone %s: no primary DNS to transfer from", z.Origin)
			continue
		}
		if err := a.createSecondaryNoPersist(z.Origin, from); err != nil {
			log.Warningf("sync zone %s: %v", z.Origin, err)
			continue
		}
		_ = a.db.UpsertZone(store.ZoneRow{
			Origin: z.Origin, Kind: zonereg.KindSecondary, Source: zonereg.SourceAdmin,
			PersistPath: filepath.Join(a.cfg.Data, persistName(z.Origin)), TransferFrom: store.JoinCSV(from),
			CreatedAt: z.CreatedAt,
		})
	}
	a.publishTSIG()
	a.publishFilter()
	a.publishTransfer()
	syncCount.WithLabelValues("ok").Inc()
	a.refreshZoneMetrics()
	if reload {
		a.pendingReload = true
	}
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
	if err := a.applySnapshot(snap); err != nil {
		return err
	}
	if a.pendingReload {
		a.pendingReload = false
		a.scheduleCorefileReload()
	}
	return nil
}

func (a *Admin) pushSnapshot() {
	// Primary does not retain member secrets, so it cannot push. Secondaries
	// pull /cluster/snapshot every 30s.
}

func (a *Admin) joinPrimary(url, token, name, dnsAddr, apiURL string) error {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	payload, _ := json.Marshal(map[string]string{
		"token": token, "name": name, "api_url": apiURL, "dns_addr": dnsAddr,
	})
	client := a.httpClient
	if client == nil || client.Timeout < time.Minute {
		c := &http.Client{Timeout: 2 * time.Minute}
		if client != nil {
			c.Transport = client.Transport
		}
		client = c
	}
	resp, err := client.Post(url+"/api/v1/cluster/join", "application/json", bytes.NewReader(payload))
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
