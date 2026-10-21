package connector

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/beeper/line/pkg/line"
)

func isCanonicalUserMID(identifier string) bool {
	normalized, valid := line.NormalizeUserMID(identifier)
	return valid && normalized == identifier
}

func normalizeUserMID(identifier string) string {
	normalized, _ := line.NormalizeUserMID(identifier)
	return normalized
}

// resolveContactIdentifier never matches display names or creates portals.
// Public IDs are resolved afresh: caching an ID->MID mapping could route a
// reassigned public ID to the wrong recipient. Contact data is cached by MID.
func (lc *LineClient) resolveContactIdentifier(ctx context.Context, identifier string) (result *line.Contact, resultErr error) {
	defer func() {
		outcome := line.LookupErrorCategory(resultErr)
		if resultErr == nil && result == nil {
			outcome = "not_found"
		}
		lookupDiagnostic(ctx, "exact", "complete", outcome, 0)
	}()
	if err := lc.checkLineCall(ctx); err != nil {
		return nil, err
	}
	identifier = strings.TrimSpace(identifier)
	if normalized, valid := line.NormalizeUserMID(identifier); valid {
		identifier = normalized
	} else {
		identifier = strings.ToLower(identifier)
	}
	if identifier == "" || strings.ContainsFunc(identifier, unicode.IsSpace) || strings.ContainsFunc(identifier, unicode.IsControl) {
		return nil, fmt.Errorf("provide a LINE public ID or user MID, not a display name")
	}
	var contact *line.Contact
	var err error
	if isCanonicalUserMID(identifier) {
		lc.cacheMu.Lock()
		cached, ok := lc.contactCache[identifier]
		lc.cacheMu.Unlock()
		if ok && cached.Contact.Mid == identifier && time.Since(cached.cachedAt) < contactCacheTTL {
			lookupDiagnostic(ctx, "exact", "cache", "hit_validated", 1)
			return &cached.Contact, nil
		}
		lookupDiagnostic(ctx, "exact", "cache", "miss_or_unusable", 0)
		if lc.isOwnMID(identifier) {
			_, profile, lookupErr := callLineResult(lc, ctx, func(client *line.Client) (*line.Profile, error) {
				return client.GetProfileContext(ctx)
			})
			err = lookupErr
			if profile != nil {
				contact = &line.Contact{Mid: profile.Mid, DisplayName: profile.DisplayName, PicturePath: profile.PicturePath}
			}
		} else {
			_, contacts, lookupErr := callLineResult(lc, ctx, func(client *line.Client) (*line.ContactsResponse, error) {
				return client.GetContactsV2Context(ctx, []string{identifier})
			})
			err = lookupErr
			if contacts != nil {
				lookupDiagnostic(ctx, "exact", "contacts", "decoded", len(contacts.Contacts))
				for key, wrapper := range contacts.Contacts {
					if normalizeUserMID(key) == identifier {
						if contact != nil {
							lookupDiagnostic(ctx, "exact", "validation", "ambiguous_keys", 0)
							return nil, fmt.Errorf("LINE lookup returned ambiguous user MID entries")
						}
						contact = &wrapper.Contact
					}
				}
			}
			if err == nil && contact == nil {
				lookupDiagnostic(ctx, "exact", "fallback", "buddy_attempted", 0)
				_, buddy, buddyErr := callLineResult(lc, ctx, func(client *line.Client) (*line.BuddyProfile, error) {
					return client.GetBuddyProfileContext(ctx, identifier)
				})
				err = buddyErr
				if buddy != nil && buddy.Mid != "" {
					contact = &line.Contact{Mid: buddy.Mid, DisplayName: buddy.DisplayName, PicturePath: buddy.PicturePath}
				}
			}
		}
	} else {
		lookupDiagnostic(ctx, "exact", "cache", "public_id_bypass", 0)
		_, contact, err = callLineResult(lc, ctx, func(client *line.Client) (*line.Contact, error) {
			return client.FindContactByUseridContext(ctx, identifier)
		})
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("LINE identifier lookup failed (the account may restrict ID lookup): %w", err)
	}
	if contact == nil || contact.Mid == "" {
		lookupDiagnostic(ctx, "exact", "validation", "missing_mid_or_contact", 0)
		return nil, nil
	}
	// Normalize legacy hex only; opaque gateway IDs must remain byte-identical.
	lookupDiagnostic(ctx, "exact", "mid_shape", line.UserMIDState(contact.Mid), 0)
	contact.Mid = normalizeUserMID(contact.Mid)
	if !isCanonicalUserMID(contact.Mid) || isCanonicalUserMID(identifier) && contact.Mid != identifier {
		outcome := "invalid_mid_shape"
		if isCanonicalUserMID(contact.Mid) {
			outcome = "requested_mid_mismatch"
		}
		lookupDiagnostic(ctx, "exact", "validation", outcome, 0)
		return nil, fmt.Errorf("LINE lookup returned an invalid or mismatched user MID")
	}
	lc.setCachedContact(contact.Mid, *contact)
	lookupDiagnostic(ctx, "exact", "validation", "accepted_and_cached", 1)
	return contact, nil
}
