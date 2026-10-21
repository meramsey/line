package line

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	gen "github.com/beeper/line/pkg"
)

type QRCodeResponse struct {
	CallbackURL                string `json:"callbackUrl"`
	LongPollingMaxCount        int    `json:"longPollingMaxCount"`
	LongPollingIntervalSeconds int    `json:"longPollingIntervalSec"`
}

func (c *Client) StartQRLogin(ctx context.Context) (session string, qr *QRCodeResponse, err error) {
	if c.LoginAttempt != nil {
		c.LoginAttempt.Close()
	}
	if err = ctx.Err(); err != nil {
		return
	}
	runner, err := gen.NewRunner()
	if err != nil {
		return "", nil, err
	}
	attemptCtx, cancel := context.WithCancel(ctx)
	attempt := &LoginAttempt{Runner: runner, Context: attemptCtx, cancel: cancel}
	c.LoginAttempt = attempt
	context.AfterFunc(attemptCtx, runner.Close)
	defer func() {
		if attemptCtx.Err() != nil {
			err = attemptCtx.Err()
		}
		if err != nil {
			attempt.Close()
			session, qr = "", nil
		}
	}()
	secret, err := runner.GenerateE2EESecret()
	if err != nil {
		return "", nil, err
	}
	var created struct {
		Session string `json:"authSessionId"`
	}
	if err = c.callQRRPC(attemptCtx, "createSession", "", 0, struct{}{}, &created); err != nil {
		return
	}
	if created.Session == "" {
		return "", nil, fmt.Errorf("QR login returned an empty session")
	}
	session = created.Session
	qr = &QRCodeResponse{}
	if err = c.callQRRPC(attemptCtx, "createQrCode", "", 0, map[string]string{"authSessionId": session}, qr); err != nil {
		return
	}
	callback, parseErr := url.Parse(qr.CallbackURL)
	if parseErr != nil || callback.Scheme == "" || (callback.Host == "" && callback.Path == "") {
		return "", nil, fmt.Errorf("QR login returned an invalid callback URL")
	}
	publicKey, err := hex.DecodeString(secret.PublicKeyHex)
	if err != nil || len(publicKey) != 32 {
		return "", nil, fmt.Errorf("QR login generated an invalid public key")
	}
	query := callback.Query()
	query.Set("secret", base64.StdEncoding.EncodeToString(publicKey))
	query.Set("e2eeVersion", "1")
	callback.RawQuery = query.Encode()
	qr.CallbackURL = callback.String()
	return
}

func (c *Client) CheckQRCodeVerifiedContext(ctx context.Context, session string, timeout time.Duration) error {
	return c.callQRRPC(ctx, "checkQrCodeVerified", session, timeout, map[string]string{"authSessionId": session}, nil)
}

func (c *Client) VerifyQRCertificate(ctx context.Context, session, certificate string) (bool, error) {
	err := c.callQRRPC(ctx, "verifyCertificate", "", 0, map[string]string{"authSessionId": session, "certificate": certificate}, nil)
	var response *qrRPCError
	if errors.As(err, &response) && response.httpStatus == http.StatusBadRequest {
		return false, nil
	}
	return err == nil, err
}

type qrRPCError struct {
	method         string
	httpStatus     int
	code           *int
	statusCode     *int
	exceptionCode  *int
	exceptionType  string
	exceptionLabel string
	reason         string
	alert          string
}

// secondaryQrCodeErrorLabels maps the fixed error codes of
// SecondaryQrCodeException to their schema labels. The codes are only
// interpreted for that exception class, since other classes reuse the numbers.
var secondaryQrCodeErrorLabels = map[int]string{
	0:   "INTERNAL_ERROR",
	1:   "ILLEGAL_ARGUMENT",
	2:   "VERIFICATION_FAILED",
	3:   "NOT_ALLOWED_QR_CODE_LOGIN",
	4:   "VERIFICATION_NOTICE_FAILED",
	5:   "RETRY_LATER",
	100: "INVALID_CONTEXT",
	101: "APP_UPGRADE_REQUIRED",
}

func (e *qrRPCError) Error() string {
	message := fmt.Sprintf("QR %s failed: HTTP %d", e.method, e.httpStatus)
	if e.code != nil {
		message += fmt.Sprintf(", code %d", *e.code)
	}
	if e.statusCode != nil {
		message += fmt.Sprintf(", status %d", *e.statusCode)
	}
	if e.exceptionCode != nil {
		message += fmt.Sprintf(", exception code %d", *e.exceptionCode)
		if e.exceptionLabel != "" {
			message += " (" + e.exceptionLabel + ")"
		}
	}
	if e.exceptionType != "" {
		message += ", exception type " + e.exceptionType
	}
	if e.reason != "" {
		message += ", reason " + e.reason
	}
	if e.alert != "" {
		message += ", alert " + e.alert
	}
	return message
}

// QRFailureDetail describes a rejected QR login request. Every field is a fixed
// schema label or an allowlisted category, never raw provider text.
type QRFailureDetail struct {
	Method         string
	HTTPStatus     int
	ResponseCode   int
	ExceptionType  string
	ExceptionCode  int
	ExceptionLabel string
	Reason         string
	Alert          string
}

// QRLoginFailureDetail extracts the sanitized details of a rejected QR login
// request. It reports false for errors that did not come from a QR request.
func QRLoginFailureDetail(err error) (QRFailureDetail, bool) {
	var response *qrRPCError
	if !errors.As(err, &response) {
		return QRFailureDetail{}, false
	}
	detail := QRFailureDetail{
		Method:         response.method,
		HTTPStatus:     response.httpStatus,
		ExceptionType:  response.exceptionType,
		ExceptionLabel: response.exceptionLabel,
		Reason:         response.reason,
		Alert:          response.alert,
	}
	if response.code != nil {
		detail.ResponseCode = *response.code
	}
	if response.exceptionCode != nil {
		detail.ExceptionCode = *response.exceptionCode
	}
	return detail, true
}

func IsQRLoginPollExpired(err error) bool {
	var response *qrRPCError
	return errors.As(err, &response) && response.code != nil && *response.code == 10052 && response.statusCode != nil && *response.statusCode == 410
}

func (c *Client) CreatePinCode(ctx context.Context, session string) (string, error) {
	var result struct {
		Pin string `json:"pinCode"`
	}
	if err := c.callQRRPC(ctx, "createPinCode", "", 0, map[string]string{"authSessionId": session}, &result); err != nil {
		return "", err
	}
	if result.Pin == "" {
		return "", fmt.Errorf("QR login returned an empty PIN")
	}
	return result.Pin, nil
}

func (c *Client) CheckPinCodeVerifiedContext(ctx context.Context, session string) error {
	return c.callQRRPC(ctx, "checkPinCodeVerified", session, 110*time.Second, map[string]string{"authSessionId": session}, nil)
}

func (c *Client) QRCodeLoginV2(ctx context.Context, session string) (*LoginResult, error) {
	var result struct {
		LoginResult
		LastBindTimestamp string      `json:"lastBindTimestamp"`
		MetaData          LoginResult `json:"metaData"`
	}
	args := map[string]any{"authSessionId": session, "systemName": "CHROMEOS", "modelName": "CHROME", "autoLoginIsRequired": false}
	if err := c.callQRRPC(ctx, "qrCodeLoginV2", "", 0, args, &result); err != nil {
		return nil, err
	}
	res := &result.LoginResult
	res.Attempt = c.LoginAttempt
	res.LastPrimaryBindTime = result.LastBindTimestamp
	res.EncryptedKeyChain = result.MetaData.EncryptedKeyChain
	res.E2EEPublicKey = result.MetaData.E2EEPublicKey
	res.E2EEVersion = result.MetaData.E2EEVersion
	res.E2EEKeyID = result.MetaData.E2EEKeyID
	if res.TokenV3IssueResult != nil && res.TokenV3IssueResult.AccessToken != "" {
		res.AuthToken = res.TokenV3IssueResult.AccessToken
	}
	if res.AuthToken == "" {
		return nil, fmt.Errorf("QR login returned no access token")
	}
	c.AccessToken = res.AuthToken
	return res, nil
}

func (c *Client) callQRRPC(ctx context.Context, method, session string, pollTimeout time.Duration, args, out any) error {
	if c.LoginAttempt == nil {
		return fmt.Errorf("QR login has not started")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.LoginAttempt.Context, cancel)
	defer stop()
	defer cancel()
	if err := c.LoginAttempt.Context.Err(); err != nil {
		return err
	}
	body, err := json.Marshal([]any{args})
	if err != nil {
		return err
	}
	service := "SecondaryQrCodeLoginService"
	if pollTimeout > 0 {
		service = "SecondaryQrCodeLoginPermitNoticeService"
	}
	path := "/api/talk/thrift/LoginQrCode/" + service + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://line-chrome-gw.line-apps.com"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("x-line-chrome-version", ExtensionVersion)
	req.Header.Set("x-lal", "en_US")
	if session != "" {
		req.Header.Set("X-Line-Session-ID", session)
	}
	if pollTimeout > 0 {
		req.Header.Set("X-LST", strconv.FormatInt(pollTimeout.Milliseconds(), 10))
	}
	signature, err := c.LoginAttempt.Runner.GetSignature(path, string(body), "")
	if err != nil {
		return err
	}
	req.Header.Set("x-hmac", signature)
	httpClient := *c.HTTPClient
	if pollTimeout > 0 {
		httpClient.Timeout = pollTimeout + 10*time.Second
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var wrapper struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wrapper)
	if resp.StatusCode != http.StatusOK || (wrapper.Code != nil && *wrapper.Code != 0) {
		response := &qrRPCError{method: method, httpStatus: resp.StatusCode, code: wrapper.Code}
		var details struct {
			StatusCode *int   `json:"statusCode"`
			Code       *int   `json:"code"`
			Name       string `json:"name"`
			Reason     string `json:"reason"`
			Alert      string `json:"alertMessage"`
		}
		if json.Unmarshal(wrapper.Data, &details) == nil {
			response.statusCode = details.StatusCode
			response.exceptionCode = details.Code
			// Provider strings may contain credentials. Retain fixed schema labels
			// and allowlisted reasons only, never arbitrary message/reason values.
			switch details.Name {
			case "TalkException", "LoginException", "SecondaryQrCodeLoginException", "SecondaryQrCodeException":
				response.exceptionType = details.Name
			case "":
			default:
				response.exceptionType = "other"
			}
			response.reason = safeLoginPollingReason(details.Reason)
			if details.Reason != "" && response.reason == "" {
				response.reason = "redacted"
			}
			response.alert = safeLoginPollingReason(details.Alert)
			if details.Alert != "" && response.alert == "" {
				response.alert = "redacted"
			}
			if details.Name == "SecondaryQrCodeException" && details.Code != nil {
				response.exceptionLabel = secondaryQrCodeErrorLabels[*details.Code]
			}
		}
		return response
	}
	if decodeErr != nil || wrapper.Code == nil {
		return fmt.Errorf("QR %s returned an invalid response", method)
	}
	if out != nil {
		if err = json.Unmarshal(wrapper.Data, out); err != nil {
			return fmt.Errorf("QR %s returned invalid data", method)
		}
	}
	return ctx.Err()
}
