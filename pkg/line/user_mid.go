package line

import "strings"

// NormalizeUserMID validates the supported user identity representations. Legacy
// hexadecimal MIDs are case-insensitive. Chrome gateway opaque IDs are not: keep
// their bytes intact for RPCs, cache keys and portal identity. Never decode an
// opaque ID into a purported legacy MID or trim a server-returned value.
// See readme/LOOKUP_DIAGNOSTICS.md for the live round-trip evidence.
func NormalizeUserMID(mid string) (string, bool) {
	if len(mid) == 44 && mid[0] == 'U' {
		for _, ch := range mid[1:] {
			if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
				return mid, false
			}
		}
		return mid, true
	}
	if len(mid) != 33 || mid[0] != 'u' && mid[0] != 'U' {
		return mid, false
	}
	for _, ch := range mid[1:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return mid, false
		}
	}
	return strings.ToLower(mid), true
}
