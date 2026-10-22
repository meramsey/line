package line

import (
	"strings"
	"testing"
)

func TestNormalizeUserMID(t *testing.T) {
	opaque := "U" + "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-aDEFg"
	legacy := "u0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, input, want string
		valid             bool
	}{
		{"opaque", opaque, opaque, true},
		{"distinct opaque case", "U" + strings.ToLower(opaque[1:]), "U" + strings.ToLower(opaque[1:]), true},
		{"legacy", legacy, legacy, true},
		{"legacy uppercase", strings.ToUpper(legacy), legacy, true},
		{"lowercase opaque prefix", strings.ToLower(opaque), "", false},
		{"wrong kind", "C" + opaque[1:], "", false},
		{"short", opaque[:43], "", false},
		{"long", opaque + "a", "", false},
		{"space", opaque[:43] + " ", "", false},
		{"newline", opaque[:43] + "\n", "", false},
		{"padding", opaque[:43] + "=", "", false},
		{"slash", opaque[:43] + "/", "", false},
		{"plus", opaque[:43] + "+", "", false},
		{"unicode", opaque[:42] + "é", "", false},
		{"legacy nonhex", legacy[:32] + "g", "", false},
		{"empty", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := NormalizeUserMID(tc.input)
			if valid != tc.valid || valid && got != tc.want || !valid && got != tc.input {
				t.Fatalf("normalize=%q,%v", got, valid)
			}
		})
	}
	if UserMIDState(opaque) != "opaque_user_mid" || UserMIDState(opaque[:43]+"=") != "invalid_alphabet" || UserMIDState(strings.ToLower(opaque)) != "invalid_prefix" {
		t.Fatal("incorrect opaque MID diagnostic")
	}
}
