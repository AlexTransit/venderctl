package state

import (
	"fmt"
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

func (c *Config) WebRoutePrefix() string {
	p := NormalizeWebPath(c.Web.Path)
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
	return NormalizeWebPath(c.Web.Path)
}

func (c *Config) WebAuthCallbackURL(token string) string {
	base := strings.TrimRight(c.Web.BaseURL, "/")
	return fmt.Sprintf("%s%s?token=%s", base, c.WebPathWithPrefix("/auth/callback"), token)
}
