package admin

import (
	"io"
	"net/http"
	"strings"

	"github.com/skymoore/coredns-ez/admin/store"
)

func (a *Admin) maybeProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Role != roleSecondary {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		if stayOnSecondary(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		url, err := a.db.Meta(store.MetaPrimaryURL)
		if err != nil || url == "" {
			writeError(w, http.StatusServiceUnavailable, "primary unreachable")
			return
		}
		dst := url + r.URL.Path
		if r.URL.RawQuery != "" {
			dst += "?" + r.URL.RawQuery
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, dst, r.Body)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		req.Header = r.Header.Clone()
		resp, err := a.httpClient.Do(req)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "primary unreachable")
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

func stayOnSecondary(path string) bool {
	if strings.HasPrefix(path, "/api/v1/auth/") ||
		strings.HasPrefix(path, "/api/v1/cluster/snapshot") ||
		strings.HasPrefix(path, "/api/v1/cluster/connect") ||
		strings.HasPrefix(path, "/api/v1/cluster/primary-dns") ||
		strings.HasPrefix(path, "/api/v1/backup") ||
		strings.HasPrefix(path, "/api/v1/update") {
		return true
	}
	// Force AXFR/IXFR on this node. The primary's copy of the zone is a
	// primary, so proxying POST .../transfer yields "not a secondary".
	if strings.HasPrefix(path, "/api/v1/zones/") && strings.HasSuffix(path, "/transfer") {
		return true
	}
	return false
}
