package transport

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/ZheglY/family_tree_app/internal/core/errors"
	"github.com/ZheglY/family_tree_app/internal/features/auth/ratelimit"
)

var (
	noAccountRule       = ratelimit.Rule{}
	registerIPRule      = ratelimit.Rule{Limit: 10, Window: time.Hour}
	registerAccountRule = ratelimit.Rule{Limit: 3, Window: time.Hour}
	loginIPRule         = ratelimit.Rule{Limit: 30, Window: time.Minute}
	loginAccountRule    = ratelimit.Rule{Limit: 10, Window: time.Minute}
	verifyIPRule        = ratelimit.Rule{Limit: 20, Window: time.Minute}
	refreshIPRule       = ratelimit.Rule{Limit: 60, Window: time.Minute}
	forgotIPRule        = ratelimit.Rule{Limit: 20, Window: time.Hour}
	forgotAccountRule   = ratelimit.Rule{Limit: 3, Window: time.Hour}
	resetIPRule         = ratelimit.Rule{Limit: 10, Window: time.Minute}
	changeIPRule        = ratelimit.Rule{Limit: 20, Window: time.Minute}
	changeAccountRule   = ratelimit.Rule{Limit: 5, Window: time.Minute}
)

type AuthRateLimiter interface {
	Allow(context.Context, string, string, ratelimit.Rule) (ratelimit.Decision, error)
}

func (h *Handler) allowAuthAttempt(
	rw http.ResponseWriter,
	request *http.Request,
	operation string,
	accountSubject string,
	ipRule ratelimit.Rule,
	accountRule ratelimit.Rule,
) bool {
	ipSubject := remoteIPAddress(request)
	if ipSubject == "" {
		ipSubject = "unknown"
	}
	if !h.allowRateLimit(
		rw,
		request,
		operation+":ip",
		ipSubject,
		ipRule,
	) {
		return false
	}
	if accountRule.Limit == 0 {
		return true
	}
	accountSubject = strings.ToLower(strings.TrimSpace(accountSubject))
	if accountSubject == "" {
		accountSubject = "invalid"
	}
	return h.allowRateLimit(
		rw,
		request,
		operation+":account",
		accountSubject,
		accountRule,
	)
}

func (h *Handler) allowRateLimit(
	rw http.ResponseWriter,
	request *http.Request,
	scope string,
	subject string,
	rule ratelimit.Rule,
) bool {
	decision, err := h.rateLimiter.Allow(request.Context(), scope, subject, rule)
	if err != nil {
		writeError(
			rw,
			request,
			fmt.Errorf("%w: auth rate limiter: %v", apperrors.ErrServiceUnavailable, err),
			"Authentication service is temporarily unavailable",
		)
		return false
	}
	if decision.Allowed {
		return true
	}

	retrySeconds := int(math.Ceil(decision.RetryAfter.Seconds()))
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	rw.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
	writeError(
		rw,
		request,
		apperrors.ErrTooManyRequests,
		"Too many authentication attempts; try again later",
	)
	return false
}
