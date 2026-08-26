package admin

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

const filterMaxBody = 32 << 20

var filterSkipNames = map[string]struct{}{
	"localhost.":             {},
	"localhost.localdomain.": {},
	"local.":                 {},
	"broadcasthost.":         {},
	"ip6-localhost.":         {},
	"ip6-loopback.":          {},
	"ip6-allnodes.":          {},
	"ip6-allrouters.":        {},
	"ip6-allhosts.":          {},
	"localdomain.":           {},
}

type parsedPattern struct {
	Pattern  string
	KidsOnly bool
}

func parseFilterPattern(raw string) (parsedPattern, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return parsedPattern{}, fmt.Errorf("empty domain")
	}
	kids := false
	if strings.HasPrefix(s, "*.") {
		kids = true
		s = s[2:]
	}
	s = strings.Trim(s, ".")
	if s == "" || s == "*" {
		return parsedPattern{}, fmt.Errorf("invalid domain")
	}
	if strings.Contains(s, "*") {
		return parsedPattern{}, fmt.Errorf("wildcard is only allowed as a leading *. ")
	}
	if net.ParseIP(s) != nil {
		return parsedPattern{}, fmt.Errorf("not a domain")
	}
	name := strings.ToLower(dns.CanonicalName(s))
	if _, ok := dns.IsDomainName(name); !ok {
		return parsedPattern{}, fmt.Errorf("invalid domain")
	}
	if dns.CountLabel(name) < 2 {
		return parsedPattern{}, fmt.Errorf("domain needs at least two labels")
	}
	if _, skip := filterSkipNames[name]; skip {
		return parsedPattern{}, fmt.Errorf("reserved name")
	}
	return parsedPattern{Pattern: name, KidsOnly: kids}, nil
}

func parseFilterList(r io.Reader) []parsedPattern {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	seen := map[string]parsedPattern{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line[0] == '#' || line[0] == '!' || line[0] == '[' {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 && !strings.HasPrefix(line, "||") {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
		}
		cand := ""
		if strings.HasPrefix(line, "||") {
			rest := strings.TrimPrefix(line, "||")
			if i := strings.IndexByte(rest, '^'); i >= 0 {
				rest = rest[:i]
			}
			if i := strings.IndexAny(rest, "/$"); i >= 0 {
				rest = rest[:i]
			}
			cand = rest
		} else {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if ip := net.ParseIP(fields[0]); ip != nil {
				if len(fields) < 2 {
					continue
				}
				cand = fields[1]
			} else {
				cand = fields[0]
			}
		}
		p, err := parseFilterPattern(cand)
		if err != nil {
			continue
		}
		if existing, ok := seen[p.Pattern]; ok && !existing.KidsOnly {
			continue
		}
		seen[p.Pattern] = p
	}
	out := make([]parsedPattern, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out
}

func validateFilterURL(raw string, allowLocal bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url must be http or https")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid url")
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback())
	if allowLocal && loopback {
		return u.String(), nil
	}
	if loopback || host == "metadata.google.internal" {
		return "", fmt.Errorf("url host is not public")
	}
	if ip != nil {
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return "", fmt.Errorf("url host is not public")
		}
	}
	return u.String(), nil
}

func feedNameFromURL(raw, fallback string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		if fallback != "" {
			return fallback
		}
		return "list"
	}
	name := strings.TrimSpace(fallback)
	if name != "" {
		return name
	}
	base := u.Path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSpace(base)
	if base != "" && base != "/" {
		return base
	}
	return u.Hostname()
}

func rulesFromParsed(action, source string, parsed []parsedPattern) []store.FilterRule {
	out := make([]store.FilterRule, 0, len(parsed))
	for _, p := range parsed {
		out = append(out, store.FilterRule{
			Action:   action,
			Pattern:  p.Pattern,
			KidsOnly: p.KidsOnly,
			Source:   source,
		})
	}
	return out
}
