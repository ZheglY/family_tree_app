package transport

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type CookieConfig struct {
	Name     string `envconfig:"REFRESH_COOKIE_NAME" default:"family_tree_refresh"`
	Path     string `envconfig:"REFRESH_COOKIE_PATH" default:"/api/v1/auth"`
	Domain   string `envconfig:"REFRESH_COOKIE_DOMAIN"`
	Secure   bool   `envconfig:"REFRESH_COOKIE_SECURE" default:"false"`
	SameSite string `envconfig:"REFRESH_COOKIE_SAME_SITE" default:"strict"`
}

type RefreshCookie struct {
	config   CookieConfig
	sameSite http.SameSite
}

func LoadCookieConfig() (CookieConfig, error) {
	var config CookieConfig
	if err := envconfig.Process("AUTH", &config); err != nil {
		return CookieConfig{}, fmt.Errorf("process auth cookie config: %w", err)
	}
	if strings.TrimSpace(config.Name) == "" || !strings.HasPrefix(config.Path, "/") {
		return CookieConfig{}, fmt.Errorf("auth refresh cookie name and absolute path are required")
	}

	return config, nil
}

func NewRefreshCookie(config CookieConfig) (*RefreshCookie, error) {
	var sameSite http.SameSite
	switch strings.ToLower(strings.TrimSpace(config.SameSite)) {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "lax":
		sameSite = http.SameSiteLaxMode
	case "none":
		if !config.Secure {
			return nil, fmt.Errorf("SameSite=None refresh cookie requires Secure=true")
		}
		sameSite = http.SameSiteNoneMode
	default:
		return nil, fmt.Errorf("unsupported auth refresh cookie SameSite value %q", config.SameSite)
	}

	return &RefreshCookie{config: config, sameSite: sameSite}, nil
}

func (c *RefreshCookie) Set(
	rw http.ResponseWriter,
	value string,
	expiresAt time.Time,
	now time.Time,
) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if !now.IsZero() {
		maxAge = int(expiresAt.Sub(now).Seconds())
	}
	if maxAge < 1 {
		maxAge = 1
	}

	http.SetCookie(rw, &http.Cookie{
		Name:     c.config.Name,
		Value:    value,
		Path:     c.config.Path,
		Domain:   c.config.Domain,
		Expires:  expiresAt.UTC(),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.config.Secure,
		SameSite: c.sameSite,
	})
}

func (c *RefreshCookie) Clear(rw http.ResponseWriter) {
	http.SetCookie(rw, &http.Cookie{
		Name:     c.config.Name,
		Value:    "",
		Path:     c.config.Path,
		Domain:   c.config.Domain,
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.config.Secure,
		SameSite: c.sameSite,
	})
}

func (c *RefreshCookie) Read(request *http.Request) (string, bool) {
	cookie, err := request.Cookie(c.config.Name)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}
