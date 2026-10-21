package connector

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/beeper/line/pkg/line"
)

const (
	// loginRateLimitCooldown is how long the bridge stops starting new login
	// attempts for an account after LINE rejected a login as rate limited.
	// Repeated attempts keep the account locked, so the bridge waits instead
	// of retrying.
	loginRateLimitCooldown = 15 * time.Minute
	// loginRetryLaterCooldown is the shorter wait used when LINE only asked the
	// client to come back later.
	loginRetryLaterCooldown = 5 * time.Minute
	// loginRetryLaterMessage is shown when LINE asked the client to come back
	// later instead of naming a rate limit.
	loginRetryLaterMessage = "LINE asked us to try the sign-in again later. Please wait a few minutes before trying again."
)

// loginRateLimitKind classifies how strongly a login rejection looks like an
// account login rate limit.
type loginRateLimitKind int

const (
	loginRateLimitNone loginRateLimitKind = iota
	loginRateLimitLocked
	loginRateLimitRetryLater
)

// loginCooldown tracks when each account may try to sign in again. It is
// in-memory on purpose: the limit is enforced by LINE, so the bridge only
// needs to avoid making it worse.
type loginCooldown struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// block stops new login attempts for the account until the given time.
func (c *loginCooldown) block(userID string, until time.Time) {
	if c == nil || userID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.until == nil {
		c.until = make(map[string]time.Time)
	}
	if current, ok := c.until[userID]; !ok || until.After(current) {
		c.until[userID] = until
	}
}

// remaining returns how long the account still has to wait.
func (c *loginCooldown) remaining(userID string) time.Duration {
	if c == nil || userID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.until[userID]
	if !ok {
		return 0
	}
	remaining := time.Until(until)
	if remaining <= 0 {
		delete(c.until, userID)
		return 0
	}
	return remaining
}

// classifyLoginRateLimit reports whether a login rejection is an account login
// rate limit. LINE does not name the condition in every response, so the
// explicit signals are used: HTTP 429, the fixed "too many login attempts"
// reason, or a request to come back later.
func classifyLoginRateLimit(err error) loginRateLimitKind {
	if err == nil {
		return loginRateLimitNone
	}
	if detail, ok := line.QRLoginFailureDetail(err); ok {
		if detail.HTTPStatus == http.StatusTooManyRequests {
			return loginRateLimitLocked
		}
		if isLoginRateLimitText(detail.Reason) || isLoginRateLimitText(detail.Alert) {
			return loginRateLimitLocked
		}
		if detail.ExceptionLabel == "RETRY_LATER" {
			return loginRateLimitRetryLater
		}
		return loginRateLimitNone
	}
	details := parseLoginErrorDetails(err)
	if details.HTTPStatus == http.StatusTooManyRequests {
		return loginRateLimitLocked
	}
	if isLoginRateLimitText(details.ErrorReason) || isLoginRateLimitText(details.ErrorMessage) {
		return loginRateLimitLocked
	}
	return loginRateLimitNone
}

func isLoginRateLimitText(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "too many login attempts", "blocked user":
		return true
	}
	return false
}

// noteLoginRateLimit records a rate limit rejection so the next attempt waits
// instead of extending the lock.
func (ll *LineEmailLogin) noteLoginRateLimit(err error) {
	if ll.cooldown == nil || ll.User == nil {
		return
	}
	now := time.Now()
	switch classifyLoginRateLimit(err) {
	case loginRateLimitLocked:
		ll.cooldown.block(string(ll.User.MXID), now.Add(loginRateLimitCooldown))
	case loginRateLimitRetryLater:
		ll.cooldown.block(string(ll.User.MXID), now.Add(loginRetryLaterCooldown))
	}
}

// loginCooldownError asks the user to wait while the account is still inside a
// rate limit window.
func (ll *LineEmailLogin) loginCooldownError() error {
	if ll.cooldown == nil || ll.User == nil {
		return nil
	}
	remaining := ll.cooldown.remaining(string(ll.User.MXID))
	if remaining <= 0 {
		return nil
	}
	return ErrLoginRateLimited.WithMessage(
		"LINE is limiting how often this account can sign in. Please try again in %s.",
		formatLoginWait(remaining),
	)
}

func formatLoginWait(remaining time.Duration) string {
	minutes := int((remaining + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}
