package line

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// These are synthetic parser/privacy probes, not captured LINE responses.
func TestLookupDiagnosticShapes(t *testing.T) {
	for _, tc := range []struct {
		name, body, path, kind, state string
		size                          int
	}{
		{"empty", `{"code":0,"data":{}}`, "$.data", "object", "", 0},
		{"array", `{"data":[]}`, "$.data", "array", "", 0},
		{"null", `{"data":null}`, "$.data", "null", "", 0},
		{"nested contact", `{"data":{"contact":{"mid":"u0123456789abcdef0123456789abcdef"}}}`, "$.data.contact.mid", "string", "canonical", 33},
		{"nested result", `{"data":{"result":{"userMid":"U0123456789ABCDEF0123456789ABCDEF"}}}`, "$.data.result.userMid", "string", "valid_case_variant", 33},
		{"buddy", `{"data":{"buddyMid":"redacted-probe"}}`, "$.data.buddyMid", "string", "invalid_length", 14},
		{"number", `{"data":{"mid":123}}`, "$.data.mid", "number", "", 0},
		{"bool", `{"data":{"mid":false}}`, "$.data.mid", "bool", "", 0},
		{"map", `{"data":{"contacts":{"PRIVATE-ID":{"contact":{"displayName":"PRIVATE-NAME"}}}}}`, "$.data.contacts", "object", "", 1},
		{"contacts array", `{"data":{"contacts":["PRIVATE-ID"]}}`, "$.data.contacts", "array", "", 1},
		{"contacts null", `{"data":{"contacts":null}}`, "$.data.contacts", "null", "", 0},
		{"contacts scalar", `{"data":{"contacts":"PRIVATE-ID"}}`, "$.data.contacts", "unexpected_scalar", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape, _, outcome := lookupResponseShape([]byte(tc.body))
			if outcome != "json" {
				t.Fatal(outcome)
			}
			found := false
			for _, node := range shape {
				if node.Path == tc.path {
					found = true
					if node.Type != tc.kind || node.MIDState != tc.state || node.Size != tc.size {
						t.Fatalf("node = %+v", node)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.path, shape)
			}
			encoded, _ := json.Marshal(shape)
			for _, private := range []string{"PRIVATE-ID", "PRIVATE-NAME", "0123456789", "redacted-probe"} {
				if strings.Contains(string(encoded), private) {
					t.Fatalf("private value leaked: %s", encoded)
				}
			}
		})
	}
	shape, code, outcome := lookupResponseShape([]byte(`{"code":403,"message":"PRIVATE-ERROR","token":"PRIVATE-TOKEN","PRIVATE-KEY":true,"data":{"mid":""}}`))
	encoded, _ := json.Marshal(shape)
	if code == nil || *code != 403 || outcome != "api_error" || strings.Contains(string(encoded), "PRIVATE") || shape[0].OtherFields != 2 {
		t.Fatalf("unsafe/incomplete diagnostic: %s %v %s", encoded, code, outcome)
	}
	if _, _, outcome = lookupResponseShape([]byte("PRIVATE invalid JSON")); outcome != "invalid_json" {
		t.Fatal(outcome)
	}
}

func TestLookupDiagnosticMIDStates(t *testing.T) {
	for mid, want := range map[string]string{
		"": "empty", "u123": "invalid_length", "c0123456789abcdef0123456789abcdef": "invalid_prefix",
		"u0123456789abcdef0123456789abcdeg": "invalid_hex", "u0123456789abcdef0123456789abcdef": "canonical",
		"U0123456789abcdef0123456789abcdef": "valid_case_variant",
	} {
		if got := UserMIDState(mid); got != want {
			t.Fatalf("state=%s want=%s", got, want)
		}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{{nil, "ok"}, {context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"}, {errors.New("PRIVATE"), "error"}} {
		if got := LookupErrorCategory(tc.err); got != tc.want {
			t.Fatal(got)
		}
	}
}

func TestLookupDiagnosticRPCOptInAndRedaction(t *testing.T) {
	for _, status := range []int{200, 403} {
		var events []LookupDiagnostic
		ctx := WithLookupDiagnostics(context.Background(), func(d LookupDiagnostic) { events = append(events, d) })
		client := NewClient("PRIVATE-TOKEN")
		client.HTTPClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":7,"message":"PRIVATE-ERROR","data":{"mid":"PRIVATE-MID","displayName":"PRIVATE-NAME"}}`))}, nil
		})
		_, _ = client.FindContactByUseridContext(ctx, "PRIVATE-PUBLIC-ID")
		if len(events) != 2 || events[0].Stage != "request" || events[1].HTTPStatus != status || events[1].APICode == nil || *events[1].APICode != 7 {
			t.Fatalf("events=%+v", events)
		}
		encoded, _ := json.Marshal(events)
		if strings.Contains(string(encoded), "PRIVATE") {
			t.Fatalf("leak: %s", encoded)
		}
		_, _ = client.FindContactByUseridContext(context.Background(), "PRIVATE-PUBLIC-ID")
		_, _ = client.callRPCContext(ctx, "AuthService", "login", "PRIVATE-PASSWORD")
		if len(events) != 2 {
			t.Fatal("diagnostics not scoped to opted-in lookup operations")
		}
		client.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })
		_, _ = client.FindContactByUseridContext(ctx, "PRIVATE-PUBLIC-ID")
		if events[len(events)-1].Outcome != "canceled" {
			t.Fatal(events)
		}
	}
}
