package transport

import "testing"

func TestRefreshCookieRejectsInsecureSameSiteNone(t *testing.T) {
	if _, err := NewRefreshCookie(CookieConfig{
		Name:     "refresh",
		Path:     "/api/v1/auth",
		SameSite: "none",
	}); err == nil {
		t.Fatal("NewRefreshCookie() error = nil, want error")
	}
}
