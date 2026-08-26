package admin

import (
	"net"
	"net/http"
	"strings"

	"github.com/coredns/coredns/plugin/pkg/parse"
	"github.com/coredns/coredns/plugin/pkg/transport"
)

func normalizeTransferAddr(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" || strings.Contains(raw, "/") {
		return "", errBadTransfer
	}
	normalized, err := parse.HostPort(raw, transport.Port)
	if err != nil {
		return "", errBadTransfer
	}
	host, _, err := net.SplitHostPort(normalized)
	if err != nil || net.ParseIP(host) == nil {
		return "", errBadTransfer
	}
	return normalized, nil
}

func normalizeTransferList(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, raw := range in {
		addr, err := normalizeTransferAddr(raw)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out, nil
}

func (a *Admin) publishTransfer() {
	if a.xferHub == nil {
		return
	}
	a.xferHub.SetExtra(a.db.TransferTo())
}

func (a *Admin) handleGetTransfer(w http.ResponseWriter, _ *http.Request) {
	core := []string{}
	if a.xferHub != nil {
		core = a.xferHub.Corefile()
	}
	extra := a.db.TransferTo()
	writeJSON(w, http.StatusOK, map[string]any{
		"to":        extra,
		"corefile":  core,
		"effective": uniqueTransfer(append(append([]string{}, core...), extra...)),
	})
}

func (a *Admin) handlePutTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To []string `json:"to"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	addrs, err := normalizeTransferList(body.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, "IP or IP:port only; no CIDR, no *")
		return
	}
	if err := a.db.SetTransferTo(addrs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.publishTransfer()
	_, _ = a.db.BumpGeneration()
	go a.pushSnapshot()
	a.db.Audit(actorFrom(r).Username, "transfer.update", "", strings.Join(addrs, ","))
	a.handleGetTransfer(w, r)
}

func (a *Admin) appendTransferAddr(raw string) {
	addr, err := normalizeTransferAddr(raw)
	if err != nil {
		return
	}
	added, err := a.db.AddTransferTo(addr)
	if err != nil || !added {
		return
	}
	a.publishTransfer()
}
