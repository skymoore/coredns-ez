package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
	dnsupdatepersist "github.com/skymoore/coredns-ez/dns-update-persistent"
	"github.com/skymoore/coredns-ez/internal/zonereg"
)

var (
	reViewHeader = regexp.MustCompile(`^view\s+(\S+)`)
	reIncidr     = regexp.MustCompile(`(?i)incidr\s*\([^,]+,\s*'([^']+)'`)
	reMutable    = regexp.MustCompile(`(?i)^\s*mutable\s+(.+)$`)
	reFileArg    = regexp.MustCompile(`(?i)^\s*file\s+(\S+)`)
	rePersistArg = regexp.MustCompile(`(?i)^\s*persist\s+(\S+)`)
	reFromArg    = regexp.MustCompile(`(?i)transfer\s+from\s+(.+)$`)
	reACLOpen    = regexp.MustCompile(`(?i)^acl(?:\s|$)`)
	reViewOpen   = regexp.MustCompile(`(?i)^view\s+\S+`)
)

type zonefileRef struct {
	Origin  string
	View    string
	Path    string
	Mutable []string
	Kind    string
	From    []string
	CIDRs   []string
}

func (a *Admin) importCorefileZones() error {
	conf := corefilePath()
	body, err := os.ReadFile(conf)
	if err != nil {
		return nil
	}
	text := string(body)
	confDir := filepath.Dir(conf)
	if confDir == "" {
		confDir = "."
	}
	refs := extractZonefileRefs(text, confDir)
	imported := 0
	for _, ref := range refs {
		if err := a.importZonefileRef(ref); err != nil {
			log.Warningf("import %s %s: %v", ref.Origin, ref.Path, err)
			continue
		}
		imported++
	}
	if n := a.importCorefileACLs(text); n > 0 {
		_, _ = a.db.BumpGeneration()
		log.Infof("imported %d corefile ACL(s) into sqlite", n)
	}
	a.persistCorefileTSIG()
	next, changed := simplifyCorefile(text)
	if changed {
		tmp := conf + ".next"
		if err := os.WriteFile(tmp, []byte(next), 0o640); err != nil {
			return err
		}
		if err := os.Rename(tmp, conf); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		log.Infof("simplified Corefile (zones=%d); listeners and plugins only", imported)
	} else if imported > 0 {
		log.Infof("imported %d zonefiles into sqlite", imported)
	}
	return nil
}

func simplifyCorefile(text string) (string, bool) {
	next, changed := stripZonefileBlocks(text)
	if s, ok := stripPolicyStanzas(next); ok {
		next, changed = s, true
	}
	if s, ok := mergeDuplicateListeners(next); ok {
		next, changed = s, true
	}
	return next, changed
}

func (a *Admin) importZonefileRef(ref zonefileRef) error {
	origin := strings.ToLower(dns.CanonicalName(ref.Origin))
	view := strings.ToLower(strings.TrimSpace(ref.View))
	if origin == "" || ref.Path == "" {
		return nil
	}
	kind := ref.Kind
	if kind == "" {
		kind = zonereg.KindPrimary
	}
	row := store.ZoneRow{
		Origin: origin, Kind: kind, Source: zonereg.SourceAdmin,
		Mutable: strings.Join(ref.Mutable, ","), TransferFrom: store.JoinCSV(ref.From),
	}
	if a.db.HasRecords(origin, view) {
		if view != "" {
			_ = a.db.UpsertZoneView(store.ZoneView{Origin: origin, ACL: view})
		}
		return a.db.UpsertZone(row)
	}
	rrs, err := dnsupdatepersist.ReadZoneFile(ref.Path, origin)
	if err != nil {
		return err
	}
	if soaOf(rrs) == nil {
		return nil
	}
	if err := a.db.ReplaceRecords(origin, view, rrs); err != nil {
		return err
	}
	if err := a.db.UpsertZone(row); err != nil {
		return err
	}
	if view == "" {
		return nil
	}
	if _, err := a.db.GetACLByName(view); err != nil && len(ref.CIDRs) > 0 {
		if _, err := a.db.InsertACL(store.ACL{Name: view, Networks: ref.CIDRs}); err != nil {
			log.Warningf("import acl %s: %v", view, err)
		}
	}
	if err := a.db.UpsertZoneView(store.ZoneView{Origin: origin, ACL: view}); err != nil {
		return err
	}
	return a.seedPublicApex(origin, rrs)
}

func (a *Admin) importCorefileACLs(text string) int {
	n := 0
	for name, cidrs := range extractCorefileACLs(text) {
		if err := a.upsertACLNetworks(name, cidrs); err != nil {
			log.Warningf("import acl %s: %v", name, err)
			continue
		}
		n++
	}
	return n
}

func (a *Admin) upsertACLNetworks(name string, cidrs []string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := store.ValidACLName(name); err != nil {
		return err
	}
	existing, err := a.db.GetACLByName(name)
	if err != nil {
		_, err = a.db.InsertACL(store.ACL{Name: name, Networks: cidrs})
		return err
	}
	nets := append(append([]string{}, existing.Networks...), cidrs...)
	_, err = a.db.UpdateACL(name, name, nets, nil)
	return err
}

func extractCorefileACLs(text string) map[string][]string {
	out := map[string][]string{}
	add := func(name string, cidrs []string) {
		if name == "" {
			name = "internal"
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if store.ValidACLName(name) != nil || len(cidrs) == 0 {
			return
		}
		seen := map[string]bool{}
		for _, c := range out[name] {
			seen[c] = true
		}
		for _, c := range cidrs {
			c = strings.TrimSpace(c)
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			out[name] = append(out[name], c)
		}
	}
	for _, b := range splitCorefileBlocks(text) {
		view, viewCIDRs := parseView(b.body)
		allow := parseACLAllow(b.body)
		add(view, viewCIDRs)
		name := view
		if name == "" {
			name = "internal"
		}
		add(name, allow)
	}
	return out
}

func parseACLAllow(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		lower := strings.ToLower(line)
		i := strings.Index(lower, "allow net ")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len("allow net "):])
		rest = strings.TrimSuffix(rest, "}")
		out = append(out, strings.Fields(rest)...)
	}
	return out
}

func (a *Admin) persistCorefileTSIG() {
	if a.tsig == nil {
		return
	}
	have := map[string]bool{}
	keys, err := a.db.ListTSIGKeys()
	if err != nil {
		return
	}
	for _, k := range keys {
		have[k.Name] = true
	}
	for name, secret := range a.tsig.Snapshot() {
		name = dns.CanonicalName(name)
		if have[name] || secret == "" {
			continue
		}
		if _, err := a.db.CreateTSIGKey(store.TSIGKey{Name: name, Algorithm: store.TSIGAlgSHA256, Secret: secret}); err != nil {
			log.Warningf("import tsig %s: %v", name, err)
		}
	}
	a.publishTSIG()
}

func extractZonefileRefs(text, confDir string) []zonefileRef {
	var out []zonefileRef
	for _, b := range splitCorefileBlocks(text) {
		if isSnippetHeader(b.header) || isListenerHeader(b.header) {
			continue
		}
		origin := zoneHeaderOrigin(b.header)
		if origin == "" {
			continue
		}
		view, cidrs := parseView(b.body)
		mutable := parseMutableArgs(b.body)
		from := parseTransferFrom(b.body)
		kind := zonereg.KindPrimary
		if strings.Contains(b.body, "secondary-persistent") {
			kind = zonereg.KindSecondary
		}
		for _, path := range zonefilePaths(b.body) {
			p := path
			if !filepath.IsAbs(p) {
				p = filepath.Join(confDir, p)
			}
			out = append(out, zonefileRef{
				Origin: origin, View: view, Path: p, Mutable: mutable, Kind: kind, From: from, CIDRs: cidrs,
			})
		}
	}
	return out
}

func parseView(body string) (name string, cidrs []string) {
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if m := reViewHeader.FindStringSubmatch(trim); m != nil {
			name = strings.Trim(m[1], `"`)
		}
		for _, m := range reIncidr.FindAllStringSubmatch(line, -1) {
			cidrs = append(cidrs, m[1])
		}
	}
	return name, cidrs
}

func parseMutableArgs(body string) []string {
	for _, line := range strings.Split(body, "\n") {
		if m := reMutable.FindStringSubmatch(line); m != nil {
			return strings.Fields(m[1])
		}
	}
	return nil
}

func parseTransferFrom(body string) []string {
	for _, line := range strings.Split(body, "\n") {
		if m := reFromArg.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return strings.Fields(m[1])
		}
	}
	return nil
}

func zonefilePaths(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if m := reFileArg.FindStringSubmatch(trim); m != nil {
			out = append(out, m[1])
		}
		if m := rePersistArg.FindStringSubmatch(trim); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

type coreBlock struct {
	header string
	body   string
	raw    string
}

func splitCorefileBlocks(text string) []coreBlock {
	lines := strings.Split(text, "\n")
	var out []coreBlock
	i := 0
	for i < len(lines) {
		if !strings.Contains(lines[i], "{") {
			i++
			continue
		}
		header := strings.TrimSpace(lines[i])
		depth := 0
		start := i
		for i < len(lines) {
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			i++
			if depth <= 0 {
				break
			}
		}
		raw := strings.Join(lines[start:i], "\n")
		out = append(out, coreBlock{header: header, body: raw, raw: raw})
	}
	return out
}

func isSnippetHeader(header string) bool {
	h := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(header), "{"))
	return strings.HasPrefix(h, "(")
}

func zoneHeaderOrigin(header string) string {
	h := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(header), "{"))
	if h == "" || isSnippetHeader(header) || isListenerHeader(header) {
		return ""
	}
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	return strings.ToLower(dns.CanonicalName(h))
}

func blockHasZonefile(body string) bool {
	if strings.Contains(body, "dns-update-persistent") || strings.Contains(body, "secondary-persistent") {
		return true
	}
	for _, line := range strings.Split(body, "\n") {
		if reFileArg.MatchString(strings.TrimSpace(line)) || rePersistArg.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func stripZonefileBlocks(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	changed := false
	i := 0
	for i < len(lines) {
		if !strings.Contains(lines[i], "{") {
			out = append(out, lines[i])
			i++
			continue
		}
		header := strings.TrimSpace(lines[i])
		depth := 0
		start := i
		for i < len(lines) {
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			i++
			if depth <= 0 {
				break
			}
		}
		block := strings.Join(lines[start:i], "\n")
		if !isSnippetHeader(header) && !isListenerHeader(header) && blockHasZonefile(block) {
			changed = true
			continue
		}
		out = append(out, lines[start:i]...)
	}
	next := strings.Join(out, "\n")
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	return next, changed
}

func stripPolicyStanzas(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	var out []string
	changed := false
	i := 0
	for i < len(lines) {
		trim := strings.TrimSpace(lines[i])
		if (reViewOpen.MatchString(trim) || reACLOpen.MatchString(trim)) && strings.Contains(lines[i], "{") {
			depth := 0
			for i < len(lines) {
				depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
				i++
				if depth <= 0 {
					break
				}
			}
			changed = true
			continue
		}
		out = append(out, lines[i])
		i++
	}
	next := strings.Join(out, "\n")
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	return next, changed
}

func mergeDuplicateListeners(text string) (string, bool) {
	blocks := splitCorefileBlocks(text)
	if len(blocks) == 0 {
		return text, false
	}
	type slot struct {
		b     coreBlock
		inner []string
	}
	var order []slot
	idx := map[string]int{}
	changed := false
	for _, b := range blocks {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(b.header), "{"))
		if isSnippetHeader(b.header) {
			order = append(order, slot{b: b, inner: blockInner(b.raw)})
			continue
		}
		if i, ok := idx[key]; ok {
			order[i].inner = mergeInner(order[i].inner, blockInner(b.raw))
			changed = true
			continue
		}
		idx[key] = len(order)
		order = append(order, slot{b: b, inner: blockInner(b.raw)})
	}
	if !changed {
		return text, false
	}
	var b strings.Builder
	for i, s := range order {
		if i > 0 {
			b.WriteByte('\n')
		}
		header := strings.TrimRight(s.b.header, " \t")
		if !strings.HasSuffix(header, "{") {
			header += " {"
		}
		b.WriteString(header)
		b.WriteByte('\n')
		for _, line := range s.inner {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("}\n")
	}
	return b.String(), true
}

func blockInner(raw string) []string {
	lines := strings.Split(raw, "\n")
	if len(lines) < 2 {
		return nil
	}
	end := len(lines) - 1
	for end > 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	if end > 0 && strings.TrimSpace(lines[end]) == "}" {
		end--
	}
	var inner []string
	for _, line := range lines[1 : end+1] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		inner = append(inner, line)
	}
	return inner
}

func mergeInner(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range append(append([]string{}, a...), b...) {
		key := strings.TrimSpace(line)
		if key == "" {
			continue
		}
		if key != "{" && key != "}" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, line)
	}
	return out
}
