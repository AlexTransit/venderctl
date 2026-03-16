package state

import (
	"net/url"
	"path"
	"strings"
)

func NormalizeWebPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	if p == "." || p == "" {
		return "/"
	}
	return p
}

// parseWebURL разбирает web_url на origin и path.
func (c *Config) parseWebURL() (origin, routePrefix string) {
	u, err := url.Parse(strings.TrimRight(c.Web.BaseURL, "/"))
	if err != nil || u.Host == "" {
		return c.Web.BaseURL, "/"
	}
	origin = u.Scheme + "://" + u.Host
	routePrefix = NormalizeWebPath(u.Path)
	return
}

func (c *Config) WebRoutePrefix() string {
	_, p := c.parseWebURL()
	if p == "/" {
		return ""
	}
	return p
}

func (c *Config) WebPathWithPrefix(rel string) string {
	if rel == "" {
		rel = "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	return c.WebRoutePrefix() + rel
}

func (c *Config) WebRootPath() string {
	return c.WebPathWithPrefix("/")
}

func (c *Config) WebCookiePath() string {
	_, p := c.parseWebURL()
	return NormalizeWebPath(p)
}

func (c *Config) WebAuthCallbackURL(token string) string {
	origin, _ := c.parseWebURL()
	return origin + c.WebPathWithPrefix("/auth/callback") + "?token=" + token
}
