package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
)

func TestBrowserSecurityAddsHeaders(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{HSTSMaxAgeSeconds: 31536000, CORSMaxAgeSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "https://api.example.test/health/live", nil))

	checks := map[string]string{
		"Cache-Control":             "no-store",
		"Content-Security-Policy":   securityPolicy,
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"X-XSS-Protection":          "0",
	}
	for name, want := range checks {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestBrowserSecurityAllowsConfiguredCredentialedCORS(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{
		AllowedOrigins: "https://family.example, http://localhost:5173/", CORSMaxAgeSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "https://api.example.test/api/v1/trees", nil)
	request.Header.Set("Origin", "https://family.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Access-Control-Allow-Origin") != "https://family.example" ||
		recorder.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(strings.Join(recorder.Header().Values("Vary"), ","), "Origin") {
		t.Fatalf("status = %d, headers = %#v", recorder.Code, recorder.Header())
	}
}

func TestBrowserSecurityHandlesAllowedPreflightWithoutCallingRoute(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{
		AllowedOrigins: "https://family.example", CORSMaxAgeSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := browserSecurityTestHandler(middleware, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodOptions, "https://api.example.test/api/v1/trees", nil)
	request.Header.Set("Origin", "https://family.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if called || recorder.Code != http.StatusNoContent ||
		recorder.Header().Get("Access-Control-Max-Age") != "900" ||
		!strings.Contains(recorder.Header().Get("Access-Control-Allow-Methods"), http.MethodPost) {
		t.Fatalf("called = %t, status = %d, headers = %#v", called, recorder.Code, recorder.Header())
	}
}

func TestBrowserSecurityRejectsCrossSiteStateChange(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{AllowedOrigins: "https://family.example"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := browserSecurityTestHandler(middleware, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodPost, "https://api.example.test/api/v1/auth/refresh", nil)
	request.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	var envelope response.ErrorEnvelope
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if called || recorder.Code != http.StatusForbidden || envelope.Error.Code != "forbidden" ||
		recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("called = %t, status = %d, response = %#v", called, recorder.Code, envelope)
	}
}

func TestBrowserSecurityAllowsSameOriginAndNonBrowserStateChanges(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	handler := middleware(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		called++
		rw.WriteHeader(http.StatusNoContent)
	}))
	for _, origin := range []string{"https://api.example.test", ""} {
		request := httptest.NewRequest(http.MethodPost, "https://api.example.test/api/v1/auth/login", nil)
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("origin %q status = %d", origin, recorder.Code)
		}
	}
	if called != 2 {
		t.Fatalf("handler calls = %d, want 2", called)
	}
}

func TestBrowserSecurityRejectsCrossSiteFetchWithoutOrigin(t *testing.T) {
	t.Parallel()
	middleware, err := BrowserSecurity(BrowserSecurityConfig{})
	if err != nil {
		t.Fatal(err)
	}
	handler := browserSecurityTestHandler(middleware, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("route handler must not be called")
	}))
	request := httptest.NewRequest(http.MethodDelete, "https://api.example.test/api/v1/trees/id", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestBrowserSecurityRejectsInvalidConfigAndPreflight(t *testing.T) {
	t.Parallel()
	for _, origins := range []string{"*", "null", "https://example.test/path", "https://:443", "file://example.test"} {
		if _, err := BrowserSecurity(BrowserSecurityConfig{AllowedOrigins: origins}); err == nil {
			t.Errorf("allowed origins %q accepted", origins)
		}
	}
	if _, err := BrowserSecurity(BrowserSecurityConfig{HSTSMaxAgeSeconds: -1}); err == nil {
		t.Error("negative HSTS max age accepted")
	}

	middleware, err := BrowserSecurity(BrowserSecurityConfig{AllowedOrigins: "https://family.example"})
	if err != nil {
		t.Fatal(err)
	}
	handler := browserSecurityTestHandler(middleware, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("route handler must not be called")
	}))
	request := httptest.NewRequest(http.MethodOptions, "https://api.example.test/api/v1/trees", nil)
	request.Header.Set("Origin", "https://family.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "x-unexpected-header")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func browserSecurityTestHandler(browserSecurity Middleware, next http.Handler) http.Handler {
	return ChainMiddleware(next, Logger(logger.NewNop()), browserSecurity)
}
