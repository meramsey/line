package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/beeper/line/pkg/line"
)

// Synthetic fault injection proves the current error-masking path. These are
// not live captures and do not establish what the reported account returned.
func TestLookupDiagnosticsSearchAndStartChat(t *testing.T) {
	for _, tc := range []struct {
		name, body, outcome string
		wantErr             bool
	}{
		{"empty", `{"code":0,"data":{}}`, "missing_mid_or_contact", false},
		{"null", `{"code":0,"data":null}`, "missing_mid_or_contact", false},
		{"missing MID", `{"code":0,"data":{"displayName":"PRIVATE-NAME"}}`, "missing_mid_or_contact", false},
		{"malformed MID", `{"code":0,"data":{"mid":"PRIVATE-MID"}}`, "invalid_mid_shape", true},
		{"API error", `{"code":7,"message":"PRIVATE-ERROR"}`, "error", true},
		{"restricted probe", `{"code":1,"message":"ID search restricted"}`, "error", true},
		{"unverified nested shape", `{"code":0,"data":{"contact":{"mid":"` + resolutionMID + `"}}}`, "missing_mid_or_contact", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			lc := newResolutionClient(t, func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.URL.Path)
				if strings.HasSuffix(req.URL.Path, "/findContactByUserid") {
					return resolutionResponse(tc.body), nil
				}
				if strings.HasSuffix(req.URL.Path, "/getAllContactIds") {
					return resolutionResponse(`{"code":0,"data":[]}`), nil
				}
				t.Fatalf("unexpected RPC: %s", req.URL.Path)
				return nil, nil
			})
			var logs bytes.Buffer
			lc.UserLogin.Bridge.Log = zerolog.New(&logs)
			lc.UserLogin.Bridge.Network.(*LineConnector).Config.LookupDiagnostics = true
			result, err := lc.ResolveIdentifier(context.Background(), "PRIVATE-PUBLIC-ID", true)
			if result != nil || (err != nil) != tc.wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			results, err := lc.SearchUsers(context.Background(), "PRIVATE-PUBLIC-ID")
			if err != nil || len(results) != 0 {
				t.Fatalf("search=%+v err=%v", results, err)
			}
			if len(calls) != 3 {
				t.Fatalf("calls=%v", calls)
			}
			if !strings.Contains(logs.String(), `"outcome":"`+tc.outcome+`"`) || !strings.Contains(logs.String(), "display_name_attempted") {
				t.Fatalf("missing diagnostic: %s", logs.String())
			}
			if strings.Contains(logs.String(), "PRIVATE") || strings.Contains(logs.String(), resolutionMID) {
				t.Fatalf("private data leaked: %s", logs.String())
			}
			assertPortalCount(t, lc, 0)
			logs.Reset()
			lc.UserLogin.Bridge.Network.(*LineConnector).Config.LookupDiagnostics = false
			_, _ = lc.ResolveIdentifier(context.Background(), "PRIVATE-PUBLIC-ID", true)
			if logs.Len() != 0 {
				t.Fatalf("diagnostics enabled by default: %s", logs.String())
			}
		})
	}
}

func TestLookupDiagnosticsCache(t *testing.T) {
	for _, state := range []string{"canonical", "mismatched", "miss"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			lc := newResolutionClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				return resolutionResponse(`{"code":0,"data":{"contacts":{"` + resolutionMID + `":{"contact":{"mid":"` + resolutionMID + `"}}}}}`), nil
			})
			if state == "canonical" {
				lc.setCachedContact(resolutionMID, line.Contact{Mid: resolutionMID})
			}
			if state == "mismatched" {
				lc.setCachedContact(resolutionMID, line.Contact{Mid: resolutionOwnMID})
			}
			var events []line.LookupDiagnostic
			ctx := line.WithLookupDiagnostics(context.Background(), func(d line.LookupDiagnostic) { events = append(events, d) })
			result, err := lc.ResolveIdentifier(ctx, resolutionMID, true)
			if err != nil || result == nil || result.UserID != resolutionMID {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			encoded, _ := json.Marshal(events)
			want := "miss_or_unusable"
			if state == "canonical" {
				want = "hit_validated"
				if calls != 0 {
					t.Fatal("cache hit called LINE")
				}
			} else if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if !strings.Contains(string(encoded), want) {
				t.Fatalf("events=%s", encoded)
			}
		})
	}
}
