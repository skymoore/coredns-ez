package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/skymoore/coredns-plugins/admin/store"
)

func (a *Admin) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(a.metricsMW)
	if len(a.cfg.CORS) > 0 {
		r.Use(a.corsMW)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/", a.handleIndex)
		r.Get("/health", a.handleHealth)
		r.Get("/auth/config", a.handleAuthConfig)
		r.Post("/auth/login", a.handleLogin)
		r.Get("/auth/oidc/login", a.handleOIDCLogin)
		r.Get("/auth/oidc/callback", a.handleOIDCCallback)
		r.Post("/cluster/join", a.handleClusterJoin)
		r.Post("/cluster/connect", a.handleClusterConnect)
		r.Get("/cluster/snapshot", a.handleClusterSnapshot)
		r.Put("/cluster/snapshot", a.handleClusterSnapshotApply)

		r.Group(func(r chi.Router) {
			r.Use(a.authRequired)
			r.Use(a.maybeProxy)
			r.Get("/auth/me", a.handleMe)
			r.Post("/auth/logout", a.handleLogout)
			r.Get("/node", a.handleNode)
			r.Get("/metrics", requireRole(store.RoleViewer, a.handleMetrics))
			r.Get("/audit", requireRole(store.RoleViewer, a.handleAudit))
			r.Get("/openapi.json", a.handleOpenAPI)

			r.Get("/users", requireRole(store.RoleAdmin, a.handleListUsers))
			r.Post("/users", requireRole(store.RoleAdmin, a.handleCreateUser))
			r.Get("/users/{id}", requireRole(store.RoleAdmin, a.handleGetUser))
			r.Patch("/users/{id}", requireRole(store.RoleAdmin, a.handlePatchUser))
			r.Delete("/users/{id}", requireRole(store.RoleAdmin, a.handleDeleteUser))

			r.Get("/tokens", requireRole(store.RoleOperator, a.handleListTokens))
			r.Post("/tokens", requireRole(store.RoleOperator, a.handleCreateToken))
			r.Delete("/tokens/{id}", requireRole(store.RoleOperator, a.handleDeleteToken))

			r.Get("/zones", requireRole(store.RoleViewer, a.handleListZones))
			r.Post("/zones", requireRole(store.RoleOperator, a.handleCreateZone))
			r.Get("/zones/{origin}", requireRole(store.RoleViewer, a.handleGetZone))
			r.Patch("/zones/{origin}", requireRole(store.RoleOperator, a.handlePatchZone))
			r.Delete("/zones/{origin}", requireRole(store.RoleOperator, a.handleDeleteZone))
			r.Post("/zones/{origin}/notify", requireRole(store.RoleOperator, a.handleNotifyZone))
			r.Post("/zones/{origin}/transfer", requireRole(store.RoleOperator, a.handleTransferZone))
			r.Get("/zones/{origin}/records", requireRole(store.RoleViewer, a.handleListRecords))
			r.Post("/zones/{origin}/records", requireRole(store.RoleOperator, a.handleAddRecord))
			r.Put("/zones/{origin}/records", requireRole(store.RoleOperator, a.handleReplaceRecords))
			r.Delete("/zones/{origin}/records", requireRole(store.RoleOperator, a.handleDeleteRecords))

			r.Get("/cluster", requireRole(store.RoleAdmin, a.handleGetCluster))
			r.Post("/cluster/join-tokens", requireRole(store.RoleAdmin, a.handleCreateJoinToken))
			r.Get("/cluster/members", requireRole(store.RoleAdmin, a.handleListMembers))
			r.Delete("/cluster/members/{id}", requireRole(store.RoleAdmin, a.handleDeleteMember))
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		})
	})
	r.Get("/*", a.handleUI)
	r.Get("/", a.handleUI)
	r.NotFound(a.handleUI)
	return r
}

func (a *Admin) metricsMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		httpCount.WithLabelValues(r.Method, strconv.Itoa(ww.Status())).Inc()
		_ = start
	})
}

func (a *Admin) corsMW(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range a.cfg.CORS {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] || allowed["*"] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Admin) handleIndex(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"name": "coredns-admin",
		"api":  "/api/v1",
		"doh":  "/dns-query",
		"ui":   "/",
	})
}

func (a *Admin) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "role": a.cfg.Role})
}

func (a *Admin) handleNode(w http.ResponseWriter, _ *http.Request) {
	nodeID, _ := a.db.Meta(store.MetaNodeID)
	clusterID, _ := a.db.Meta(store.MetaClusterID)
	adv, _ := a.db.Meta(store.MetaAdvertise)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":            nodeID,
		"role":          a.cfg.Role,
		"cluster_id":    clusterID,
		"advertise_dns": adv,
		"generation":    a.db.Generation(),
	})
}
