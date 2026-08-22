package middleware

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/transport/http/response"
)

const (
	securityPolicy = "default-src 'none'; base-uri 'none'; frame-ancestors 'none'"
)

var corsMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodPost: {}, http.MethodPut: {},
	http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
}

var corsHeaders = map[string]struct{}{
	"authorization": {}, "content-type": {}, "x-request-id": {}, "x-csrf-protection": {},
}

type BrowserSecurityConfig struct {
	AllowedOrigins    string
	HSTSMaxAgeSeconds int
	CORSMaxAgeSeconds int
}

func BrowserSecurity(config BrowserSecurityConfig) (Middleware, error) {
	if config.HSTSMaxAgeSeconds < 0 || config.CORSMaxAgeSeconds < 0 {
		return nil, fmt.Errorf("browser security max ages cannot be negative")
	}
	allowedOrigins, err := parseAllowedOrigins(config.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, request *http.Request) {
			setBrowserSecurityHeaders(rw.Header(), config.HSTSMaxAgeSeconds)
			sourceOrigin, sourcePresent, err := requestSourceOrigin(request)
			if err != nil {
				writeBrowserSecurityError(rw, request, "Request origin is invalid")
				return
			}
			allowed := sourcePresent && originAllowed(request, sourceOrigin, allowedOrigins)
			if sourcePresent && allowed {
				setCORSResponseHeaders(rw.Header(), sourceOrigin)
			}

			if isCORSPreflight(request) {
				if !allowed || !validPreflight(request) {
					writeBrowserSecurityError(rw, request, "CORS preflight is not allowed")
					return
				}
				setPreflightHeaders(rw.Header(), config.CORSMaxAgeSeconds)
				rw.WriteHeader(http.StatusNoContent)
				return
			}

			if unsafeMethod(request.Method) &&
				((sourcePresent && !allowed) ||
					(!sourcePresent && strings.EqualFold(request.Header.Get("Sec-Fetch-Site"), "cross-site"))) {
				writeBrowserSecurityError(rw, request, "Cross-site state change is not allowed")
				return
			}
			next.ServeHTTP(rw, request)
		})
	}, nil
}

func setBrowserSecurityHeaders(header http.Header, hstsMaxAgeSeconds int) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", securityPolicy)
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("X-XSS-Protection", "0")
	if hstsMaxAgeSeconds > 0 {
		header.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(hstsMaxAgeSeconds)+"; includeSubDomains")
	}
}

func requestSourceOrigin(request *http.Request) (string, bool, error) {
	if value := strings.TrimSpace(request.Header.Get("Origin")); value != "" {
		origin, err := normalizeOrigin(value)
		return origin, true, err
	}
	if value := strings.TrimSpace(request.Referer()); value != "" {
		reference, err := url.Parse(value)
		if err != nil {
			return "", true, err
		}
		origin, err := normalizeOrigin(reference.Scheme + "://" + reference.Host)
		return origin, true, err
	}
	return "", false, nil
}

func originAllowed(request *http.Request, source string, configured map[string]struct{}) bool {
	if _, exists := configured[source]; exists {
		return true
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	target, err := normalizeOrigin(scheme + "://" + request.Host)
	return err == nil && source == target
}

func parseAllowedOrigins(value string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		origin, err := normalizeOrigin(candidate)
		if err != nil {
			return nil, fmt.Errorf("invalid HTTP allowed origin %q: %w", candidate, err)
		}
		result[origin] = struct{}{}
	}
	return result, nil
}

func normalizeOrigin(value string) (string, error) {
	if value == "" || value == "null" || strings.ContainsAny(value, " \t\r\n") {
		return "", fmt.Errorf("origin is empty, opaque or contains whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Opaque != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin must contain only an HTTP scheme and authority")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("origin hostname is empty")
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return strings.ToLower(parsed.Scheme) + "://" + host, nil
}

func isCORSPreflight(request *http.Request) bool {
	return request.Method == http.MethodOptions &&
		request.Header.Get("Origin") != "" &&
		request.Header.Get("Access-Control-Request-Method") != ""
}

func validPreflight(request *http.Request) bool {
	method := strings.ToUpper(strings.TrimSpace(request.Header.Get("Access-Control-Request-Method")))
	if _, exists := corsMethods[method]; !exists || method == http.MethodOptions {
		return false
	}
	for _, header := range strings.Split(request.Header.Get("Access-Control-Request-Headers"), ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" {
			continue
		}
		if _, exists := corsHeaders[header]; !exists {
			return false
		}
	}
	return true
}

func setCORSResponseHeaders(header http.Header, origin string) {
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Expose-Headers", "X-Request-ID")
	header.Add("Vary", "Origin")
}

func setPreflightHeaders(header http.Header, maxAgeSeconds int) {
	header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-CSRF-Protection, X-Request-ID")
	header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	header.Set("Access-Control-Max-Age", strconv.Itoa(maxAgeSeconds))
	header.Add("Vary", "Access-Control-Request-Method")
	header.Add("Vary", "Access-Control-Request-Headers")
}

func unsafeMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete
}

func writeBrowserSecurityError(rw http.ResponseWriter, request *http.Request, message string) {
	response.NewHTTPResponseHandler(logger.FromContext(request.Context()), rw).
		ErrorResponse(apperrors.ErrForbidden, message)
}
