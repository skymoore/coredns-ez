package admin

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/coredns/caddy"
)

// parseOIDC reads an oidc { ... } sub-block. caddy NextBlock does not nest,
// so this consumes tokens until the matching closing brace.
func parseOIDC(c *caddy.Controller) (*oidcSettings, error) {
	if !c.NextArg() {
		if !c.Next() {
			return nil, c.ArgErr()
		}
	}
	if c.Val() != "{" {
		return nil, c.Err("oidc: expected block")
	}
	oc := &oidcSettings{}
	for c.Next() {
		if c.Val() == "}" {
			break
		}
		key := c.Val()
		switch key {
		case "button_text":
			args := c.RemainingArgs()
			if len(args) == 0 {
				return nil, c.ArgErr()
			}
			oc.ButtonText = strings.Join(args, " ")
		case "issuer":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			oc.Issuer = c.Val()
		case "client_id":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			oc.ClientID = c.Val()
		case "client_secret":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			oc.ClientSecret = c.Val()
		case "redirect_url":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			oc.RedirectURL = c.Val()
		case "button_image":
			if !c.NextArg() {
				return nil, c.ArgErr()
			}
			if err := httpURL(c.Val()); err != nil {
				return nil, c.Errf("button_image: %v", err)
			}
			oc.ButtonImage = c.Val()
		default:
			return nil, c.Errf("unknown oidc property %q", key)
		}
	}
	if oc.Issuer == "" || oc.ClientID == "" || oc.ClientSecret == "" || oc.RedirectURL == "" {
		return nil, c.Err("oidc requires issuer, client_id, client_secret, redirect_url")
	}
	return oc, nil
}

func httpURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("must be an http or https URL")
	}
	return nil
}
