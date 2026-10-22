# QR/session regression audit — October 21, 2026

## Scope and evidence boundary

The operator reported that QR login worked before deployment of the public-ID
repair, and that iOS now displays an error after entering the PIN. The bridge
reports `qrCodeLoginV2`, HTTP 400, wrapper code 10051, nested exception code 100,
exception type `other`. The phone was asked for the exact on-screen error and
its LINE version: the app shows only a generic QR login failure with no code,
no text and no version information. The mobile app therefore provides no
evidence beyond the failure itself, and the bridge is the only place where the
rejection reason can be observed.

The live lookup repair is confirmed by successful resolve/search and start-chat
lookup records. This does not establish that authentication or outbound messaging
works end to end. No standalone token probe, login attempt or remote configuration
change was performed during this audit.

## Compared revisions

* `7615af2`: executable installed when this troubleshooting session began; QR
  login was completed by the operator before diagnostic deployment.
* `160ac20`: lookup diagnostic build, which reconnected that saved login.
* `05a1607`: opaque-ID repair; no QR, login, token recovery, SSE or send-path edits.
* `009d81c`: nested QR error reporting; changes only response error representation
  and equivalent expired-poll classification, not outgoing request construction.

`pkg/connector/login_qr.go` and `finishLogin` did not change across these builds.
The QR create-session, create-code, certificate, PIN, poll and token-issuance
request construction in `pkg/line/qr.go` also did not change. Header version,
authentication-session arguments, signing input, endpoint selection and the
`autoLoginIsRequired` value were retained.

`TestQRRequestContractAndMIDRepresentations` runs the mocked seven-RPC QR sequence
and checks the method, URL, session continuity, argument values, header scope,
poll durations and unmodified returned account ID. It passes both on baseline
`160ac20` in an isolated worktree and on the current source. Fixtures include
lowercase and uppercase 33-byte hex IDs and a mixed-case 44-byte opaque ID. These
tests establish request-contract compatibility, not remote protocol acceptance.

## Identity handling audit

* `NormalizeUserMID` supports both legacy `u`/`U` + 32 hexadecimal digits and the
  observed gateway `U` + 43 URL-safe ASCII characters. Legacy hex normalization
  remains; opaque IDs are preserved exactly.
* Lookup input, returned contacts, contact-map keys, cache keys and portal IDs
  retain that distinction. The normalization helper is not called by QR login.
* Login verifies the returned profile identity against the login response using
  exact equality and preserves its bytes in stored metadata and login ID.
* `makeUserID`/`makePortalID` are string type conversions; send constructs the
  recipient from the portal ID without lowercasing it. Lowercase temporaries in
  chat classification only inspect prefixes; they do not replace the recipient.
* New mocked lookup-to-send tests verify legacy and opaque recipient bytes at the
  outbound RPC boundary. Unsupported identity shape fails without invalidating the
  login or creating a portal. Unknown shapes are not accepted merely by length or
  prefix: that would undo wrong-recipient safeguards.

No newly introduced identity transformation explaining QR rejection was found.
The observed QR failure occurs before `QRCodeLoginV2` returns a successful result,
before profile verification or lookup/portal validation runs. This does not prove
that the deployment or earlier diagnostic activity could not have affected remote
session state; the initiating cause of token invalidation remains unknown.

## Account login rate limits

The operator observed that the affected account could not complete a QR login
while the account had not been used for login attempts that day, and that a
later attempt succeeded. That is consistent with an account-level login rate
limit rather than a protocol or identity defect, and it matches the repeated
attempts made during this troubleshooting session. The hypothesis is not proven:
the rejected response reported a nested exception code of 100, which the public
`SecondaryQrCodeException` enum names `INVALID_CONTEXT`, and the response did
not carry an explicit rate-limit reason.

The bridge now handles the rate limit explicitly instead of failing opaquely:

* Rate limits are recognised from the signals LINE actually returns: HTTP 429,
  the fixed "too many login attempts" reason, and the fixed `RETRY_LATER` code
  of `SecondaryQrCodeException`. A verification or credential failure is not
  treated as a rate limit.
* A rate-limited QR login is reported to the user as the standard
  "Too many login attempts" error, and a "retry later" rejection as a request
  to wait a few minutes, instead of a generic failure.
* After a rate-limited rejection the bridge stops starting new login attempts
  for that account for 15 minutes (5 minutes for a "retry later" rejection) and
  tells the user when to come back. Repeated attempts keep the account locked,
  so the bridge waits instead of retrying. The wait is in-memory and per
  account, and it is cleared once it expires.
* The same handling applies to the email login flow, because the limit is
  enforced per account rather than per sign-in method. The automatic reconnect
  after a bridge restart still makes a single attempt, because that is the only
  way to restore a saved session, and it is reported the same way if the
  account is still locked.

The cooldown lengths are conservative defaults, not measured LINE values. The
bridge cannot know how long the account stays locked, so it waits and reports
the remaining time instead of guessing an exact unlock moment.

## Structured authentication errors

LINE login failures are now normalized into a structured `AuthError` model in
`pkg/line/auth_error.go` before they reach connector logic:

* Provider-specific exception names, codes, reasons and alerts are classified
  into stable categories such as `TOO_MANY_ATTEMPTS`, `RETRY_LATER`,
  `VERIFICATION_TEMPORARILY_UNAVAILABLE`, `INVALID_CONTEXT`/`QR_CONTEXT_EXPIRED`,
  `QR_LOGIN_NOT_ALLOWED`, etc.
* Provider text is never exposed directly. Fixed schema labels and a small
  allowlist of safe reason strings are used; everything else is recorded as
  `unrecognized`/`redacted`.
* Each `AuthError` carries flags (`retry_recommended`, `temporary`,
  `user_action_required`, `bridge_failure`, `cooldown_triggered`) and a safe
  `provider_alert_status` (`recognized`, `unrecognized`, `absent`).
* The connector uses these categories to pick user-facing messages, decide
  whether a cooldown applies, and emit structured log fields. Raw error strings
  still appear in logs only at the original wrapped error level.

## Extended cooldown handling

A confirmed "Verification is temporarily unavailable" alert now triggers its
own cooldown category (`loginRateLimitVerificationUnavailable`) with a
10-minute bridge-side wait. This prevents rapid retry loops when LINE indicates
verification is temporarily unavailable, while still allowing a fresh attempt
after the cooldown expires. The category is detected both from the structured
`AuthError` and, as a fallback, from the raw provider reason text during email
login.

## Actual shortcomings corrected locally

1. The diagnostic allowlist omitted `SecondaryQrCodeException`, and did not inspect
   its `alertMessage` field. It now recognizes that fixed name and reports alerts
   through the existing fixed login-reason allowlist; other alerts are redacted.
2. The existing session-invalidated notice asserted that another client connected.
   A `V3_TOKEN_CLIENT_LOGGED_OUT` response does not identify an initiating device.
   The notice and internal error now state session invalidation without attributing
   it to another client. Invalidation/recovery behavior and key-preservation checks
   are unchanged.
3. The nested error code was reported as a bare number, which is ambiguous because
   the numeric codes belong to the exception class. The bridge now labels the
   fixed `SecondaryQrCodeException` codes (`INVALID_CONTEXT`, `RETRY_LATER`,
   `NOT_ALLOWED_QR_CODE_LOGIN`, `VERIFICATION_FAILED`,
   `VERIFICATION_NOTICE_FAILED`, `APP_UPGRADE_REQUIRED`, `ILLEGAL_ARGUMENT`,
   `INTERNAL_ERROR`) and leaves the number unlabelled for every other class.
4. QR rejections were returned to the client as a raw error, so the only place
   the reason appeared was the server log. Since the mobile app shows no detail,
   the rejection reason is now also surfaced in the login error with fixed
   copy, and the sanitized details (method, HTTP status, response code, exception
   class, code, label, allowlisted reason and alert) are written as structured
   log fields. Provider text is never quoted to the user.
5. An account login rate limit was indistinguishable from any other rejection.
   It is now detected, reported, and followed by a per-account wait so the
   bridge does not extend the lock by retrying.

These are diagnostic/user-facing corrections, not a verified fix for iOS PIN
rejection. Regression tests retain strict privacy and authentication-failure checks.

## Public protocol references (not official LINE documentation)

Two reverse-engineered definitions agree that **within SecondaryQrCodeException**
code 100 means `INVALID_CONTEXT`, and that the exception contains `alertMessage`:

* https://github.com/DeachSword/CHRLINE-Thrift/blob/49aed159e08d8db368add63fddc6c63562187068/subs/Types.thrift
* https://github.com/DeachSword/CHRLINE-Thrift/blob/49aed159e08d8db368add63fddc6c63562187068/subs/Exceptions.thrift
* https://github.com/Shivelight/line-messenger-protocol/blob/65681da9301a65e6103ad441718fdc89e5aa46f0/thrift/SecondaryQrCodeServices/enums.thrift
* https://github.com/Shivelight/line-messenger-protocol/blob/65681da9301a65e6103ad441718fdc89e5aa46f0/thrift/SecondaryQrCodeServices/exceptions.thrift

The live exception class was redacted to `other`; this mapping must therefore not
be represented as a confirmed interpretation of that response yet. Neither the
mapping nor an `INVALID_CONTEXT` label establishes why the context was rejected.
Android-oriented alternate secure-QR implementations are not evidence to change
this iOS/Chrome gateway flow without matching Chrome protocol evidence.

The code labels are applied only when the response names
`SecondaryQrCodeException`, because the same numbers mean different things in
other exception classes. The mobile app shows no error text, so the labels are
the only interpretation available until an instrumented attempt reports the
real class.

## Required next observation

The phone cannot provide the reason: it shows only a generic QR login failure
with no code, text or version. The next observation therefore has to come from
the bridge itself, which requires one authorized instrumented QR attempt with a
build that carries the new diagnostic. That attempt must report the exact fixed
exception class, the labelled code and the allowlisted alert category, and it
must not reuse the token in a separate client or request credentials/QR images.
A rate-limited attempt is expected to be reported as such instead, and the
bridge waits afterwards instead of retrying. A controlled baseline comparison
would require explicit deployment approval; no rollback or additional
authentication attempt has been performed as part of this audit.
