package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/skymoore/coredns-ez/admin/store"
)

const maxCoreFileBytes = 8 << 20

type corefileAdapt struct {
	DB          string
	Data        string
	PrimaryDNS  string // host:port the secondary AXFRs from
	PrimaryIP   string // bind address on the primary, if known
	SelfIP      string
	RedirectURL string
}

var (
	reBind       = regexp.MustCompile(`^(\s*bind\s+)(\S+)(.*)$`)
	reRole       = regexp.MustCompile(`^(\s*role\s+)primary\s*$`)
	reAdvertise  = regexp.MustCompile(`^(\s*)advertise\s+(\S+)\s*$`)
	reRedirect   = regexp.MustCompile(`^(\s*redirect_url\s+)\S+\s*$`)
	reDB         = regexp.MustCompile(`^(\s*db\s+)\S+\s*$`)
	reData       = regexp.MustCompile(`^(\s*data\s+)\S+\s*$`)
	reFilePlugin = regexp.MustCompile(`^(\s*)file\s+(\S+)\s*$`)
)

func hostPart(hp string) string {
	hp = strings.TrimSpace(hp)
	if hp == "" {
		return ""
	}
	host, _, ok := strings.Cut(hp, "://")
	if ok {
		hp = host
	}
	if i := strings.LastIndex(hp, ":"); i > 0 {
		return hp[:i]
	}
	return hp
}

func adaptCorefileForSecondary(src string, opt corefileAdapt) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines)+8)
	depth := 0
	skipIxfr := false
	i := 0
	for i < len(lines) {
		line := lines[i]
		trim := strings.TrimSpace(line)
		open := strings.Count(line, "{")
		close := strings.Count(line, "}")

		switch {
		case reRole.MatchString(line):
			line = reRole.ReplaceAllString(line, "${1}secondary")
		case strings.HasPrefix(strings.TrimSpace(line), "bootstrap_admin"):
			i++
			continue
		case reAdvertise.MatchString(line):
			line = reAdvertise.ReplaceAllString(line, "${1}dns ${2}")
		case opt.RedirectURL != "" && reRedirect.MatchString(line):
			line = reRedirect.ReplaceAllString(line, "${1}"+opt.RedirectURL)
		case opt.DB != "" && reDB.MatchString(line):
			line = reDB.ReplaceAllString(line, "${1}"+opt.DB)
		case opt.Data != "" && reData.MatchString(line):
			line = reData.ReplaceAllString(line, "${1}"+opt.Data)
		case opt.SelfIP != "" && opt.PrimaryIP != "" && opt.SelfIP != opt.PrimaryIP && reBind.MatchString(line):
			line = rewriteBindIP(line, opt.PrimaryIP, opt.SelfIP)
		case trim == "dns-update-persistent {":
			path, n := dnsUpdateFilePath(lines, i)
			i = n
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			out = append(out, secondaryPersistBlock(indent, opt.PrimaryDNS, path)...)
			skipIxfr = true
			depth += open - close
			continue
		case reFilePlugin.MatchString(trim) && !strings.Contains(trim, "{"):
			sub := reFilePlugin.FindStringSubmatch(line)
			indent, path := sub[1], sub[2]
			out = append(out, secondaryPersistBlock(indent, opt.PrimaryDNS, path)...)
			skipIxfr = true
			i++
			continue
		case skipIxfr && trim == "ixfr":
			i++
			continue
		}
		if trim != "ixfr" {
			skipIxfr = false
		}
		out = append(out, line)
		depth += open - close
		i++
	}
	adapted := strings.Join(out, "\n")
	stripped, _ := stripZonefileBlocks(adapted)
	return stripped
}

const (
	clusterBegin = "# coredns-ez-cluster-begin"
	clusterEnd   = "# coredns-ez-cluster-end"
)

func isListenerHeader(header string) bool {
	h := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(header), "{"))
	if h == "." || h == ".:53" {
		return true
	}
	for _, p := range []string{"https://", "http://", "tls://", "quic://", "grpc://"} {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	return false
}

// clusteredBlocks keeps snippets and named zone servers from an adapted
// primary Corefile, dropping admin listeners and catch-all recursion blocks.
func clusteredBlocks(adapted string) string {
	lines := strings.Split(adapted, "\n")
	var b strings.Builder
	i := 0
	for i < len(lines) {
		trim := strings.TrimSpace(lines[i])
		if !strings.Contains(lines[i], "{") {
			i++
			continue
		}
		header := trim
		depth := 0
		start := i
		for i < len(lines) {
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			i++
			if depth <= 0 {
				break
			}
		}
		if isListenerHeader(header) {
			continue
		}
		block := strings.Join(lines[start:i], "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		b.WriteString(block)
		b.WriteByte('\n')
	}
	return b.String()
}

func stripClusterSection(local string) string {
	s := local
	for {
		start := strings.Index(s, clusterBegin)
		end := strings.Index(s, clusterEnd)
		if start < 0 || end < 0 || end < start {
			return strings.TrimRight(s, "\n") + "\n"
		}
		s = s[:start] + s[end+len(clusterEnd):]
	}
}

func injectQstat(text string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return text, false
	}
	lines := strings.Split(text, "\n")
	var out []string
	changed := false
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.Count(line, "{") == 0 {
			out = append(out, line)
			i++
			continue
		}
		depth := 0
		start := i
		for i < len(lines) {
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			i++
			if depth <= 0 {
				break
			}
		}
		block := lines[start:i]
		hdr := strings.TrimSpace(block[0])
		// Snippets like `(common) {` are imported into servers. Putting
		// qstat in both the snippet and the server loads it twice.
		if strings.HasPrefix(hdr, "(") {
			out = append(out, block...)
			continue
		}
		if blockHasDirective(block, "qstat") {
			out = append(out, block...)
			continue
		}
		indent := blockDirectiveIndent(block)
		inserted := make([]string, 0, len(block)+1)
		inserted = append(inserted, block[0], indent+"qstat")
		inserted = append(inserted, block[1:]...)
		out = append(out, inserted...)
		changed = true
	}
	return strings.Join(out, "\n"), changed
}

func blockHasDirective(block []string, dir string) bool {
	for _, l := range block {
		f := strings.Fields(strings.TrimSpace(l))
		if len(f) > 0 && f[0] == dir {
			return true
		}
	}
	return false
}

func blockDirectiveIndent(block []string) string {
	for _, l := range block[1:] {
		trim := strings.TrimSpace(l)
		if trim == "" || strings.HasPrefix(trim, "#") || trim == "}" {
			continue
		}
		return l[:len(l)-len(strings.TrimLeft(l, " \t"))]
	}
	return "\t"
}

func ensureQstatDirective(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	next, changed := injectQstat(string(b))
	if !changed {
		return false, nil
	}
	tmp := path + ".qstat-next"
	if err := os.WriteFile(tmp, []byte(next), 0o640); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}

func mergeClusteredCorefile(local, _ string) string {
	return stripClusterSection(local)
}

func remapCorePath(p, primData, primConf, localData, localConf string) string {
	p = filepath.Clean(p)
	for _, pair := range [][2]string{{primData, localData}, {primConf, localConf}} {
		from, to := pair[0], pair[1]
		if from == "" || to == "" {
			continue
		}
		from = filepath.Clean(from)
		if p == from || strings.HasPrefix(p, from+string(os.PathSeparator)) {
			return to + p[len(from):]
		}
	}
	return p
}

func corefileHasOrigin(text, origin string) bool {
	o := strings.TrimSuffix(origin, ".")
	if o == "" || text == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		h := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "{"))
		if h == o || h == o+"." {
			return true
		}
	}
	return false
}

func corefileDataDir(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if m := reData.FindStringSubmatch(line); m != nil {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

func rewriteBindIP(line, from, to string) string {
	m := reBind.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	addr := m[2]
	if addr == from || strings.HasPrefix(addr, from+":") {
		addr = strings.Replace(addr, from, to, 1)
	}
	return m[1] + addr + m[3]
}

func dnsUpdateFilePath(lines []string, start int) (path string, next int) {
	depth := 0
	i := start
	for i < len(lines) {
		depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
		if f := reFilePlugin.FindStringSubmatch(strings.TrimSpace(lines[i])); f != nil && path == "" {
			path = f[2]
		}
		i++
		if depth == 0 {
			break
		}
	}
	return path, i
}

func secondaryPersistBlock(indent, from, path string) []string {
	if from == "" {
		from = "127.0.0.1:53"
	}
	block := []string{
		indent + "secondary-persistent {",
		indent + "\ttransfer from " + from,
	}
	if path != "" {
		block = append(block, indent+"\tpersist "+path)
	} else {
		block = append(block, indent+"\tdirectory /var/lib/coredns/zones")
	}
	block = append(block, indent+"}")
	return block
}

func corefileHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func collectCorefileFiles(text, confDir string) map[string][]byte {
	out := map[string][]byte{}
	add := func(p string) {
		p = strings.Trim(p, `"'`)
		if p == "" || strings.HasPrefix(p, "{$") {
			return
		}
		if !filepath.IsAbs(p) && confDir != "" {
			p = filepath.Join(confDir, p)
		}
		p = filepath.Clean(p)
		if _, ok := out[p]; ok {
			return
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return
		}
		if st.Size() > maxCoreFileBytes {
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		out[p] = b
	}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "tls ") {
			fields := strings.Fields(trim)
			for _, f := range fields[1:] {
				add(f)
			}
			continue
		}
		if m := reFilePlugin.FindStringSubmatch(trim); m != nil {
			add(m[2])
		}
	}
	if confDir != "" {
		_ = filepath.Walk(filepath.Join(confDir, "zones"), func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && !info.IsDir() && info.Size() <= maxCoreFileBytes {
				b, err := os.ReadFile(path)
				if err == nil {
					out[path] = b
				}
			}
			return nil
		})
	}
	return out
}

func (a *Admin) attachCorefile(snap *store.Snapshot) {
	path := corefilePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(b)
	snap.Corefile = text
	snap.CorefileHash = corefileHash(text)
	files := collectCorefileFiles(text, filepath.Dir(path))
	if len(files) > 0 {
		snap.CoreFiles = files
	}
}

func (a *Admin) selfListenIP(snap store.Snapshot) string {
	id, _ := a.db.Meta(store.MetaMemberID)
	for _, m := range snap.Members {
		if id != "" && m.ID == id {
			return hostPart(m.DNSAddr)
		}
	}
	return hostPart(a.cfg.AdvertiseDNS)
}

func (a *Admin) selfAPIURL(snap store.Snapshot) string {
	id, _ := a.db.Meta(store.MetaMemberID)
	for _, m := range snap.Members {
		if id != "" && m.ID == id && m.APIURL != "" {
			return strings.TrimRight(m.APIURL, "/")
		}
	}
	return ""
}

func (a *Admin) applyCorefile(_ store.Snapshot) (reload bool, err error) {
	dest := corefilePath()
	local, err := os.ReadFile(dest)
	if err != nil {
		return false, nil
	}
	stripped := stripClusterSection(string(local))
	if stripped == string(local) {
		return false, nil
	}
	tmp := dest + ".next"
	if err := os.WriteFile(tmp, []byte(stripped), 0o640); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	log.Info("stripped leftover cluster Corefile section")
	return true, nil
}

func (a *Admin) scheduleCorefileReload() {
	if a.skipReload {
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		log.Info("restarting to load clustered Corefile")
		if supervisedRestart() {
			os.Exit(0)
		}
		os.Exit(0)
	}()
}
