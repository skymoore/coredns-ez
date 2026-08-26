package admin

import (
	"net/http"
	"os"

	"github.com/skymoore/coredns-plugins/admin/store"
)

type clusterMemberJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	APIURL   string `json:"api_url"`
	DNSAddr  string `json:"dns_addr"`
	Role     string `json:"role"`
	JoinedAt int64  `json:"joined_at"`
	LastSeen int64  `json:"last_seen"`
	Self     bool   `json:"self"`
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func memberRole(m store.Member) string {
	if m.Role != "" {
		return m.Role
	}
	return store.MemberSecondary
}

func (a *Admin) selfMemberID() string {
	if a.cfg.Role == rolePrimary {
		id, _ := a.db.Meta(store.MetaNodeID)
		return id
	}
	if id, err := a.db.Meta(store.MetaMemberID); err == nil && id != "" {
		return id
	}
	name, _ := os.Hostname()
	if name == "" {
		return ""
	}
	members, _ := a.db.ListMembers()
	for _, m := range members {
		if memberRole(m) != store.MemberPrimary && m.Name == name {
			_ = a.db.SetMeta(store.MetaMemberID, m.ID)
			return m.ID
		}
	}
	return ""
}

func (a *Admin) ensureSelfMember(apiURL string) error {
	if a.cfg.Role != rolePrimary {
		return nil
	}
	id, err := a.db.Meta(store.MetaNodeID)
	if err != nil || id == "" {
		return err
	}
	name, _ := os.Hostname()
	if name == "" {
		name = rolePrimary
	}
	adv, _ := a.db.Meta(store.MetaAdvertise)
	changed, err := a.db.UpsertRosterMember(store.Member{
		ID: id, Name: name, APIURL: apiURL, DNSAddr: adv, Role: store.MemberPrimary,
	})
	if err != nil {
		return err
	}
	if changed {
		_, _ = a.db.BumpGeneration()
	}
	return nil
}

func (a *Admin) roster(r *http.Request) []clusterMemberJSON {
	if a.cfg.Role == rolePrimary {
		if err := a.ensureSelfMember(requestBaseURL(r)); err != nil {
			log.Warningf("cluster self member: %v", err)
		}
	}
	members, _ := a.db.ListMembers()
	selfID := a.selfMemberID()
	out := make([]clusterMemberJSON, 0, len(members))
	for _, m := range members {
		out = append(out, clusterMemberJSON{
			ID: m.ID, Name: m.Name, APIURL: m.APIURL, DNSAddr: m.DNSAddr,
			Role: memberRole(m), JoinedAt: m.JoinedAt, LastSeen: m.LastSeen,
			Self: selfID != "" && m.ID == selfID,
		})
	}
	return out
}
