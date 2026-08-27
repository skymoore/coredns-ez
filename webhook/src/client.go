package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type recordJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl,omitempty"`
	Rdata string `json:"rdata"`
}

type ezClient struct {
	base  string
	token string
	http  *http.Client
}

func newClient(base, token string) *ezClient {
	return &ezClient{
		base:  strings.TrimRight(strings.TrimSpace(base), "/"),
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *ezClient) ListZones(ctx context.Context) ([]string, error) {
	var body struct {
		Zones []struct {
			Origin string `json:"origin"`
		} `json:"zones"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/zones", nil, &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Zones))
	for _, z := range body.Zones {
		if o := canonicalOrigin(z.Origin); o != "" {
			out = append(out, o)
		}
	}
	return out, nil
}

func (c *ezClient) HasTXT(ctx context.Context, origin, fqdn, value string) (bool, error) {
	q := url.Values{}
	q.Set("name", fqdn)
	q.Set("type", "TXT")
	path := recordsPath(origin) + "?" + q.Encode()
	var body struct {
		Records []recordJSON `json:"records"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &body); err != nil {
		return false, err
	}
	want := unquoteTXT(value)
	for _, rec := range body.Records {
		if !strings.EqualFold(rec.Type, "TXT") {
			continue
		}
		if unquoteTXT(rec.Rdata) == want {
			return true, nil
		}
	}
	return false, nil
}

func (c *ezClient) PutTXT(ctx context.Context, origin, fqdn, value string, ttl int) error {
	ok, err := c.HasTXT(ctx, origin, fqdn, value)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	rec := recordJSON{
		Name:  fqdn,
		Type:  "TXT",
		TTL:   uint32(ttl),
		Rdata: quoteTXT(value),
	}
	return c.do(ctx, http.MethodPost, recordsPath(origin), rec, nil)
}

func (c *ezClient) DeleteTXT(ctx context.Context, origin, fqdn, value string) error {
	rec := recordJSON{
		Name:  fqdn,
		Type:  "TXT",
		Rdata: quoteTXT(value),
	}
	err := c.do(ctx, http.MethodDelete, recordsPath(origin), rec, nil)
	if err == nil {
		return nil
	}
	// Already gone is success: Present may have been retried and CleanUp called twice.
	if strings.Contains(err.Error(), "not found") {
		return nil
	}
	return err
}

func recordsPath(origin string) string {
	return "/api/v1/zones/" + canonicalOrigin(origin) + "/records"
}

func (c *ezClient) do(ctx context.Context, method, path string, in any, out any) error {
	var rdr io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coredns-ez-webhook")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		var eb struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return fmt.Errorf("coredns-ez %s %s: %s (%s)", method, path, resp.Status, msg)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func canonicalOrigin(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func quoteTXT(s string) string {
	return strconv.Quote(unquoteTXT(s))
}

func unquoteTXT(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

func matchZone(fqdn, configured string, zones []string) (string, error) {
	fqdn = canonicalOrigin(fqdn)
	if configured != "" {
		return canonicalOrigin(configured), nil
	}
	best := ""
	for _, z := range zones {
		z = canonicalOrigin(z)
		if z == "" {
			continue
		}
		if fqdn == z || strings.HasSuffix(fqdn, "."+z) || strings.HasSuffix(fqdn, z) {
			if len(z) > len(best) {
				best = z
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("no coredns-ez zone matches %s", fqdn)
	}
	return best, nil
}
