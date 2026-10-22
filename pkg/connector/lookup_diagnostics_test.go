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

// Synthetic fault injection guards against the former error-masking path. These are
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
			if (err != nil) != tc.wantErr || len(results) != 0 {
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

// Fixtures below exercise the existing getContactsV2 contract, not an alternate
// public-ID response schema. They intentionally inject adversarial identities.
func TestSearchFallbackValidatesContacts(t *testing.T) {
	for _, tc := range []struct {
		name, key, mid     string
		duplicate, wantErr bool
	}{
		{"lowercase", resolutionMID, resolutionMID, false, false},
		{"uppercase", strings.ToUpper(resolutionMID), strings.ToUpper(resolutionMID), false, false},
		{"missing", resolutionMID, "", false, true},
		{"malformed", resolutionMID, "not-a-mid", false, true},
		{"wrong recipient", resolutionMID, resolutionOwnMID, false, true},
		{"wrong key", resolutionOwnMID, resolutionMID, false, true},
		{"unsolicited", resolutionOwnMID, resolutionOwnMID, false, true},
		{"ambiguous", resolutionMID, resolutionMID, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			lc := newResolutionClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				switch {
				case strings.HasSuffix(req.URL.Path, "/findContactByUserid"):
					return resolutionResponse(`{"code":0,"data":{}}`), nil
				case strings.HasSuffix(req.URL.Path, "/getAllContactIds"):
					return resolutionResponse(`{"code":0,"data":["` + resolutionMID + `"]}`), nil
				case strings.HasSuffix(req.URL.Path, "/getContactsV2"):
					contacts := map[string]line.ContactWrapper{tc.key: {Contact: line.Contact{Mid: tc.mid, DisplayName: "Carol"}}}
					if tc.duplicate {
						contacts[strings.ToUpper(tc.key)] = line.ContactWrapper{Contact: line.Contact{Mid: tc.mid, DisplayName: "Carol"}}
					}
					body, err := json.Marshal(map[string]any{"code": 0, "data": line.ContactsResponse{Contacts: contacts}})
					if err != nil {
						t.Fatal(err)
					}
					return resolutionResponse(string(body)), nil
				default:
					t.Fatalf("unexpected RPC: %s", req.URL.Path)
					return nil, nil
				}
			})
			results, err := lc.SearchUsers(context.Background(), "Carol")
			if (err != nil) != tc.wantErr {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			if tc.wantErr {
				if len(results) != 0 {
					t.Fatal("invalid result accepted")
				}
				if _, ok := lc.contactCache[resolutionMID]; ok {
					t.Fatal("invalid contact cached")
				}
			} else {
				if len(results) != 1 || results[0].UserID != resolutionMID {
					t.Fatalf("results=%+v", results)
				}
				resolved, err := lc.ResolveIdentifier(context.Background(), resolutionMID, true)
				if err != nil || resolved.UserID != results[0].UserID {
					t.Fatalf("cached identity differs: %+v %v", resolved, err)
				}
			}
			if calls != 3 {
				t.Fatalf("unexpected RPC count: %d", calls)
			}
			assertPortalCount(t, lc, 0)
		})
	}
}

func TestSearchReportsFallbackAPIErrors(t *testing.T) {
	for _, endpoint := range []string{"getAllContactIds", "getContactsV2"} {
		t.Run(endpoint, func(t *testing.T) {
			lc := newResolutionClient(t, func(req *http.Request) (*http.Response, error) {
				if strings.HasSuffix(req.URL.Path, "/"+endpoint) {
					return resolutionResponse(`{"code":1,"message":"restricted"}`), nil
				}
				if strings.HasSuffix(req.URL.Path, "/findContactByUserid") {
					return resolutionResponse(`{"code":0,"data":{}}`), nil
				}
				return resolutionResponse(`{"code":0,"data":["` + resolutionMID + `"]}`), nil
			})
			results, err := lc.SearchUsers(context.Background(), "Carol")
			if len(results) != 0 || err == nil {
				t.Fatalf("results=%+v err=%v", results, err)
			}
			assertPortalCount(t, lc, 0)
		})
	}
}
