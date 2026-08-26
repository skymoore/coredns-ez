package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updateRepoDefault = "skymoore/coredns-ez"

var (
	githubAPI   = "https://api.github.com"
	updateMu    sync.Mutex
	updateCache updateInfo
	updateAt    time.Time
)

type updateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	AssetURL    string `json:"-"`
	SHAURL      string `json:"-"`
	PublishedAt string `json:"published_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

func coreVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path == "github.com/coredns/coredns" {
			v := strings.TrimPrefix(bi.Main.Version, "v")
			if v != "" && v != "(devel)" {
				return v
			}
		}
		for _, d := range bi.Deps {
			if d.Path == "github.com/coredns/coredns" {
				v := strings.TrimPrefix(d.Version, "v")
				if v != "" && v != "(devel)" {
					return v
				}
			}
		}
	}
	return "1.14.7"
}

func updateRepo() string {
	if r := strings.TrimSpace(os.Getenv("COREDNS_UPDATE_REPO")); r != "" {
		return r
	}
	return updateRepoDefault
}

func (a *Admin) handleUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	info, err := fetchUpdate(false)
	if err != nil {
		info.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *Admin) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" {
		writeError(w, http.StatusBadRequest, "self-update is linux only")
		return
	}
	bin, err := executablePath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := canWriteDir(filepath.Dir(bin)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	info, err := fetchUpdate(true)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if info.AssetURL == "" {
		writeError(w, http.StatusBadRequest, "no linux asset on the latest release")
		return
	}
	if err := installRelease(info); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.db.Audit(actorFrom(r).Username, "update.apply", "", info.Latest)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting", "latest": info.Latest})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go restartAfterUpdate(bin)
}

func fetchUpdate(force bool) (updateInfo, error) {
	updateMu.Lock()
	defer updateMu.Unlock()
	cur := coreVersion()
	if !force && time.Since(updateAt) < 6*time.Hour && updateCache.Latest != "" {
		c := updateCache
		c.Current = cur
		c.Available = versionNewer(c.Latest, cur)
		return c, nil
	}
	url := githubAPI + "/repos/" + updateRepo() + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return updateInfo{Current: cur}, err
	}
	req.Header.Set("User-Agent", "coredns-ez-admin")
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return updateInfo{Current: cur}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return updateInfo{Current: cur}, fmt.Errorf("github releases: %s", resp.Status)
	}
	var rel struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return updateInfo{Current: cur}, err
	}
	info := updateInfo{Current: cur, Latest: strings.TrimPrefix(rel.TagName, "v"), PublishedAt: rel.PublishedAt}
	stem := "coredns_" + info.Latest + "_" + runtime.GOOS + "_" + runtime.GOARCH
	for _, a := range rel.Assets {
		switch a.Name {
		case stem + ".tgz":
			info.AssetURL = a.BrowserDownloadURL
		case stem + ".tgz.sha256":
			info.SHAURL = a.BrowserDownloadURL
		}
	}
	info.Available = versionNewer(info.Latest, cur)
	updateCache, updateAt = info, time.Now()
	return info, nil
}

func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func canWriteDir(dir string) error {
	f, err := os.CreateTemp(dir, ".coredns-write-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (re-run install.sh so the binary lives in /var/lib/coredns): %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

func replaceExecutable(path string, bin []byte) error {
	if err := canWriteDir(filepath.Dir(path)); err != nil {
		return err
	}
	next := path + ".next"
	if err := os.WriteFile(next, bin, 0o755); err != nil {
		return fmt.Errorf("write binary (need write access to %s): %w", path, err)
	}
	if err := os.Chmod(next, 0o755); err != nil {
		_ = os.Remove(next)
		return err
	}
	_ = exec.Command("setcap", "cap_net_bind_service=+ep", next).Run()
	bak := path + ".bak"
	_ = os.Remove(bak)
	if err := os.Rename(path, bak); err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("replace binary: %w", err)
	}
	if err := os.Rename(next, path); err != nil {
		_ = os.Rename(bak, path)
		return err
	}
	_ = exec.Command("setcap", "cap_net_bind_service=+ep", path).Run()
	return nil
}

func parentComm() string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(os.Getppid()) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func supervisedRestart() bool {
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	switch parentComm() {
	case "supervise-daemon", "systemd":
		return true
	}
	return false
}

func restartAfterUpdate(bin string) {
	time.Sleep(400 * time.Millisecond)
	if supervisedRestart() {
		os.Exit(0)
	}
	if err := execSelf(bin); err != nil {
		log.Errorf("exec new binary: %v", err)
		os.Exit(1)
	}
}

func installRelease(info updateInfo) error {
	body, err := httpGet(info.AssetURL)
	if err != nil {
		return err
	}
	if info.SHAURL != "" {
		sumb, err := httpGet(info.SHAURL)
		if err != nil {
			return fmt.Errorf("checksum: %w", err)
		}
		want := strings.Fields(string(sumb))
		if len(want) == 0 {
			return fmt.Errorf("empty checksum file")
		}
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), want[0]) {
			return fmt.Errorf("checksum mismatch")
		}
	}
	bin, err := extractCoredns(body)
	if err != nil {
		return err
	}
	path, err := executablePath()
	if err != nil {
		return err
	}
	return replaceExecutable(path, bin)
}

func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "coredns-ez-admin")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func extractCoredns(archive []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		base := filepath.Base(hdr.Name)
		if hdr.Typeflag == tar.TypeReg && (base == "coredns" || base == "coredns.exe") {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("archive has no coredns binary")
}

func versionNewer(latest, current string) bool {
	l := parseVer(latest)
	c := parseVer(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVer(s string) [3]int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	var out [3]int
	parts := strings.Split(s, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
