package admin

import (
	"net/http"
	"os"
	"strings"

	"github.com/skymoore/coredns-ez/admin/store"
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

func (a *Admin) nodeName() string {
	if n, err := a.db.Meta(store.MetaNodeName); err == nil {
		n = strings.TrimSpace(n)
		if n != "" {
			return n
		}
	}
	if n := strings.TrimSpace(os.Getenv("COREDNS_NODE_NAME")); n != "" {
		return n
	}
	n, _ := os.Hostname()
	n = strings.TrimSpace(n)
	if n == "" {
		return rolePrimary
	}
	return n
}

func hostIsLoopback(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "]"); i >= 0 {
		h = strings.Trim(h[:i+1], "[]")
	} else if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1" || h == "0.0.0.0"
}

func publicAPIURL(r *http.Request, explicit, dnsAddr string) string {
	if u := strings.TrimRight(strings.TrimSpace(explicit), "/"); u != "" && !hostIsLoopback(u) {
		return u
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	if !hostIsLoopback(host) {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		return scheme + "://" + host
	}
	if ip := hostPart(dnsAddr); ip != "" && !hostIsLoopback(ip) {
		return "http://" + ip + ":8080"
	}
	return ""
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
	name := a.nodeName()
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
