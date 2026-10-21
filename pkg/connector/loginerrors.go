package connector

import (
	"errors"
	"fmt"
	"net/http"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/line/pkg/line"
)

// genericLoginFailureReason is shown in the login form when LINE rejects the sign-in but
// gives no reason we can quote. It replaces formatting the raw Go error into the
// instructions, which leaked internal detail into user-facing copy.
const genericLoginFailureReason = "LINE rejected the sign-in. Please check your email and password and try again."

var (
	ErrLoginVerificationFailed = bridgev2.RespError{
		ErrCode:    "DEV.HIGHEST.LINE.VERIFICATION_FAILED",
		Err:        "LINE didn't confirm the verification. Please start the login again.",
		StatusCode: http.StatusBadRequest,
	}
	ErrLoginNoKeychain = bridgev2.RespError{
		ErrCode:    "DEV.HIGHEST.LINE.NO_KEYCHAIN",
		Err:        "LINE finished signing in without sending the encryption keychain. Please reconnect and complete the verification prompt in the LINE app.",
		StatusCode: http.StatusBadRequest,
	}
	ErrLoginTooManyAttempts = bridgev2.RespError{
		ErrCode:    "DEV.HIGHEST.LINE.TOO_MANY_ATTEMPTS",
		Err:        loginTooManyAttemptsReason,
		StatusCode: http.StatusTooManyRequests,
	}
	ErrLoginRateLimited = bridgev2.RespError{
		ErrCode:    "DEV.HIGHEST.LINE.RATE_LIMITED",
		Err:        "LINE is limiting how often this account can sign in. Please try again later.",
		StatusCode: http.StatusTooManyRequests,
	}
	ErrLoginRejected = bridgev2.RespError{
		ErrCode:    "DEV.HIGHEST.LINE.LOGIN_REJECTED",
		Err:        genericLoginFailureReason,
		StatusCode: http.StatusUnauthorized,
	}
	ErrLoginUnknown = bridgev2.RespError{
		ErrCode:    "M_UNKNOWN",
		Err:        "Internal error logging in to LINE",
		StatusCode: http.StatusInternalServerError,
	}
)

// wrapLineLoginError translates a LINE error into one the client can act on, keeping the
// original in the chain with %w so logs are unaffected.
func wrapLineLoginError(err error) error {
	if err == nil {
		return nil
	}
	mapped := ErrLoginUnknown
	details := parseLoginErrorDetails(err)
	reason := details.ErrorReason
	if reason == "" {
		reason = details.ErrorMessage
	}
	switch {
	case errors.Is(err, line.ErrLoginVerificationExpired):
		mapped = ErrLoginVerificationFailed
	case isBlockedUserLoginError(reason):
		mapped = ErrLoginTooManyAttempts
	case isLoginRateLimitText(reason):
		mapped = ErrLoginTooManyAttempts
	case details.HTTPStatus == http.StatusTooManyRequests:
		mapped = ErrLoginTooManyAttempts
	case details.HTTPStatus == http.StatusUnauthorized, details.HTTPStatus == http.StatusForbidden:
		mapped = ErrLoginRejected
	case reason != "":
		// LINE's own reason strings are short and user-facing, so quote them.
		mapped = ErrLoginRejected.WithMessage("%s", reason)
	}
	return fmt.Errorf("%w: %w", mapped, err)
}

// qrLoginFailureMessage maps a rejected QR request to fixed user-facing copy.
// The mobile app shows no detail for these rejections, so the bridge is the
// only place the reason can be shown. Provider text is never quoted; only fixed
// schema labels are used.
func qrLoginFailureMessage(detail line.QRFailureDetail) string {
	switch detail.ExceptionLabel {
	case "NOT_ALLOWED_QR_CODE_LOGIN":
		return "LINE doesn't allow QR sign-in for this account. Please sign in with your email and password instead."
	case "INVALID_CONTEXT":
		return "LINE rejected the QR sign-in because the sign-in context was no longer valid. Please start the sign-in again with a fresh QR code."
	case "VERIFICATION_FAILED":
		return "LINE didn't confirm the QR sign-in. Please scan the QR code again and try once more."
	case "VERIFICATION_NOTICE_FAILED":
		return "LINE didn't confirm the QR sign-in request. Please try again."
	case "APP_UPGRADE_REQUIRED":
		return "LINE requires a newer version of the app. Please update the LINE app and try again."
	case "ILLEGAL_ARGUMENT":
		return "LINE rejected the QR sign-in request. Please start the sign-in again."
	}
	return "LINE rejected the QR sign-in. Please try again."
}

// wrapQRRPCError translates a rejected QR request into a user-facing error.
// The original error stays in the chain with %w so logs are unaffected.
func wrapQRRPCError(err error) error {
	if err == nil {
		return nil
	}
	detail, ok := line.QRLoginFailureDetail(err)
	if !ok {
		return err
	}
	var mapped bridgev2.RespError
	switch classifyLoginRateLimit(err) {
	case loginRateLimitLocked:
		mapped = ErrLoginTooManyAttempts
	case loginRateLimitRetryLater:
		mapped = ErrLoginRateLimited.WithMessage("%s", loginRetryLaterMessage)
	default:
		mapped = ErrLoginRejected.WithMessage("%s", qrLoginFailureMessage(detail))
	}
	return fmt.Errorf("%w: %w", mapped, err)
}
