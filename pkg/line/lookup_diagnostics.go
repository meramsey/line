package line

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// LookupDiagnostic contains only fixed labels, counts and allowlisted field
// names. Never add request arguments, response values or error text here.
type LookupDiagnostic struct {
	Operation  string        `json:"operation"`
	Stage      string        `json:"stage"`
	Outcome    string        `json:"outcome"`
	HTTPStatus int           `json:"http_status,omitempty"`
	APICode    *int          `json:"api_code,omitempty"`
	Count      int           `json:"count"`
	Shape      []LookupShape `json:"shape,omitempty"`
}

type LookupShape struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Size        int      `json:"size"`
	Fields      []string `json:"fields,omitempty"`
	OtherFields int      `json:"other_fields,omitempty"`
	MIDState    string   `json:"mid_state,omitempty"`
}

type lookupDiagnosticKey struct{}
type lookupDiagnosticSink func(LookupDiagnostic)

func WithLookupDiagnostics(ctx context.Context, sink func(LookupDiagnostic)) context.Context {
	return context.WithValue(ctx, lookupDiagnosticKey{}, lookupDiagnosticSink(sink))
}

func HasLookupDiagnostics(ctx context.Context) bool {
	sink, _ := ctx.Value(lookupDiagnosticKey{}).(lookupDiagnosticSink)
	return sink != nil
}

func RecordLookupDiagnostic(ctx context.Context, diagnostic LookupDiagnostic) {
	if sink, _ := ctx.Value(lookupDiagnosticKey{}).(lookupDiagnosticSink); sink != nil {
		sink(diagnostic)
	}
}

// LookupErrorCategory intentionally never includes an error's message. Remote
// errors and transport errors may embed raw response bodies or request URLs.
func LookupErrorCategory(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "error"
	}
}

// UserMIDState describes shape only, not ownership or whether LINE recognizes it.
func UserMIDState(mid string) string {
	if mid == "" {
		return "empty"
	}
	if len(mid) == 44 {
		if _, valid := NormalizeUserMID(mid); valid {
			return "opaque_user_mid"
		}
		if mid[0] != 'U' {
			return "invalid_prefix"
		}
		return "invalid_alphabet"
	}
	if len(mid) != 33 {
		return "invalid_length"
	}
	lower := strings.ToLower(mid)
	if lower[0] != 'u' {
		return "invalid_prefix"
	}
	for _, ch := range lower[1:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return "invalid_hex"
		}
	}
	if mid != lower {
		return "valid_case_variant"
	}
	return "canonical"
}

func lookupOperation(service, method string) bool {
	return service == "TalkService" && (method == "findContactByUserid" || method == "getContactsV2" || method == "getAllContactIds" || method == "getProfile") || service == "BuddyService" && method == "getBuddyProfile"
}

// Field names are data too: arbitrary object keys can be MIDs, public IDs or
// secrets. Only schema labels below may leave the parser. Collection members
// are never traversed, and scalar values are never emitted.
func lookupResponseShape(body []byte) ([]LookupShape, *int, string) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, nil, "invalid_json"
	}
	var code *int
	if obj, ok := root.(map[string]any); ok {
		if value, ok := obj["code"].(float64); ok && value == float64(int(value)) {
			v := int(value)
			code = &v
		}
	}
	var shapes []LookupShape
	var walk func(any, string, int)
	walk = func(value any, path string, depth int) {
		if depth > 4 || len(shapes) >= 32 {
			return
		}
		node := LookupShape{Path: path}
		switch v := value.(type) {
		case nil:
			node.Type = "null"
		case string:
			node.Type = "string"
			if strings.HasSuffix(path, ".mid") || strings.HasSuffix(path, ".buddyMid") || strings.HasSuffix(path, ".userMid") {
				node.Size = len(v)
				node.MIDState = UserMIDState(v)
			}
		case float64:
			node.Type = "number"
		case bool:
			node.Type = "bool"
		case []any:
			node.Type = "array"
			node.Size = len(v)
		case map[string]any:
			node.Type = "object"
			node.Size = len(v)
			for key := range v {
				switch key {
				case "code", "message", "data", "result", "contact", "contacts", "profile", "mid", "buddyMid", "userMid", "userId", "displayName", "displayNameOverridden", "statusMessage", "picturePath", "error":
					node.Fields = append(node.Fields, key)
				default:
					node.OtherFields++
				}
			}
			sort.Strings(node.Fields)
		}
		shapes = append(shapes, node)
		if obj, ok := value.(map[string]any); ok {
			for _, key := range node.Fields {
				switch key {
				case "data", "result", "contact", "profile", "mid", "buddyMid", "userMid":
					walk(obj[key], path+"."+key, depth+1)
				case "contacts":
					// Count the collection without exposing its identifier-keyed entries.
					child := LookupShape{Path: path + ".contacts"}
					switch collection := obj[key].(type) {
					case map[string]any:
						child.Type = "object"
						child.Size = len(collection)
					case []any:
						child.Type = "array"
						child.Size = len(collection)
					case nil:
						child.Type = "null"
					default:
						child.Type = "unexpected_scalar"
					}
					shapes = append(shapes, child)
				}
			}
		}
	}
	walk(root, "$", 0)
	if code != nil && *code != 0 {
		return shapes, code, "api_error"
	}
	return shapes, code, "json"
}
