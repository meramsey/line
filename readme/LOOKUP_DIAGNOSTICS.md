# Public-ID lookup diagnostics

## Live finding — October 21, 2026

The diagnostic build `160ac20` reproduced the failure against the Chrome gateway
using the existing LINE login. Sanitized observations (no response bodies retained
here):

* `resolve-identifier`, 20:58:49 UTC, trace `dbcif2fdiv38efc9f480`:
  `TalkService.findContactByUserid`, HTTP 200, API code 0. Envelope fields were
  `code`, `data`, `message`; `data` was a 17-field contact object including `mid`,
  `displayName`, `picturePath`, `statusMessage`. `data.mid` was a 44-byte string.
* `search`, 20:59:10 UTC, trace `dbcif7ndiv38efc9f48g`: identical exact-response
  shape/status. Both bypassed cache and rejected `invalid_length` /
  `invalid_mid_shape`. Search then fetched three IDs and three contact wrappers
  successfully (HTTP 200, API code 0), but rejected all contact identities.
* Read-only database shape checks found the saved login and three portal IDs also
  had length 44, uppercase `U` prefixes and URL-safe ASCII alphabets.
* A read-only protocol probe repeated `findContactByUserid`, then passed its
  returned MID **unchanged** to `getContactsV2`. Both returned HTTP 200/code 0.
  The second response contained exactly one entry, with a byte-identical map key
  and `contact.mid`, and equal display name. The public lookup MID also exactly
  matched an existing portal ID. Only shape/equality booleans were exported;
  credentials were piped locally on the server, never printed or stored by the
  probe. No contacts were changed or messages sent.

This verifies that the legacy-only `u` + 32 hex rule rejects an identity actually
used by this gateway. It is not a decoder-nesting, API-permission or stale-cache
failure. Blind lowercasing would additionally alter opaque identity bytes.

The repair accepts the observed `U` + 43 URL-safe ASCII characters as an opaque,
case-sensitive user ID. It preserves legacy hexadecimal normalization, but never
lowercases or decodes opaque IDs. Requested-ID equality, contact-map key equality,
requested-batch membership and legacy-normalized duplicate checks remain enforced.
This is the observed supported shape, not a claim about every possible LINE ID
format or an undocumented encryption/encoding algorithm. Diagnostics classify it
as `opaque_user_mid`; invalid 44-byte values report prefix/alphabet failures.

Regression fixtures replace all identity/profile values with invented ones. A
44-byte fixture reproduced the exact error on baseline `160ac20` in an isolated
worktree. Tests cover public lookup, direct opaque-ID requests, cache reuse, portal
reuse, fallback matching and case-only recipient mismatches, alongside the legacy
regressions. The Matrix test double explicitly disables double puppeting so the
first room creation tests actual creation rather than a recovered mock panic.

The original diagnostic setting (absent/false) and log level (`debug`) were restored
after evidence collection; the bridge reconnected with its saved LINE login.
End-to-end verification of the fixed binary remains required. No successful live
lookup/portal workflow is claimed solely from these probes or local tests.

## Pre-capture investigation (historical)

QR login is confirmed working by the operator. The latest live report says
`start-chat ripharambe201669` returns `LINE lookup returned an invalid or
mismatched user MID`, while `search ripharambe201669` returns no results. No
response capture from that account is available locally. The previous uppercase
MID fix and the new diagnostic tests use **synthetic fixtures**, not live captures.
The live root cause remains unestablished.

The exact error is emitted in `pkg/connector/resolve_identifier.go` after decoding
a nonempty contact MID and lowercasing it. Its two conditions are:

1. Returned MID is not `u` plus 32 ASCII hexadecimal digits.
2. Input was itself a canonical MID and the returned MID differs.

The reported public ID is not a MID, so condition 2 does not apply on this code
path. Verify the deployed executable's `--version-json` before attributing this
trace to that binary. This narrows the validation branch, **not the remote response
structure or cause**. In particular, a permission restriction, alternate schema,
wrong endpoint, or unrelated user cannot be inferred from the error text.

## Command and protocol trace

* Framework `commands.fnResolveIdentifier` calls
  `provisionutil.ResolveIdentifier`, then connector `ResolveIdentifier` and
  `resolveContactIdentifier`. `resolve-identifier` performs lookup without room
  creation; `start-chat` requests room creation after successful validation.
* Framework `commands.fnSearch` calls `provisionutil.SearchUsers`, then connector
  `SearchUsers`. Its first attempt uses the **same exact resolver and decoder**.
* Public IDs: POST to `TalkService.findContactByUserid`, positional JSON argument
  `[publicID]`. Current decoder expects `{code, message, data: Contact}` and reads
  `data.mid`. It does not extract IDs from nested contacts or other fields.
  Blank/missing `mid` returns no match; non-string fields produce decoding errors;
  a nonzero code produces an API error. Missing `code` currently defaults to zero
  in the existing decoder; diagnostics separately report whether it was present.
* MID input: validated MID-keyed cache, otherwise `TalkService.getContactsV2`
  with `[{targetUserMids: [mid]}, 2]`, or `TalkService.getProfile` for self.
  `getContactsV2` expects `data.contacts[key].contact.mid`. An absent matching
  entry permits `BuddyService.getBuddyProfile(mid)`, reading `data.buddyMid`.
* Search-only fallback: `TalkService.getAllContactIds`, then `getContactsV2`
  in batches of 50; display-name substring matching is local. This is not a
  second public-ID discovery API. The previously deployed implementation discards
  exact errors and can return an empty result after this fallback. Thus an empty
  search is not proof of an empty or valid response to `findContactByUserid`.
  The diagnostic build now returns the exact error when fallback finds no matches,
  and reports contact-list/batch API errors instead of a misleading empty success.
  Fallback contacts must have canonicalizable MIDs matching both the response key
  and the requested batch, with no duplicate case-normalized keys. Malformed,
  unsolicited and ambiguous entries are rejected before caching or ghost creation.
* Public IDs always bypass cache. Validated exact contacts are cached by canonical
  MID, never by public ID. This prevents reassigned IDs using stale aliases.
* Exact validation completes before ghost/portal creation. The framework creates
  or reuses the login-scoped canonical portal and owns its locking and lifecycle.
  A failed lookup creates no portal. A Matrix room-create failure retains one
  valid retryable portal row with no room ID; deleting it would race retries.

## Minimal deployed evidence collection

An operator must deliberately install the candidate build and enable diagnostics;
development does not deploy it or contact a live account.

1. Record `matrix-line --version-json` from the deployed executable and the UTC
   time. Keep the existing logged-in session; no new QR login is needed.
2. In the bridge YAML, set only:

   ```yaml
   network:
     lookup_diagnostics: true
   ```

   Merge this key into the existing `network` mapping. Apply using the operator's
   normal configuration/restart procedure. The option defaults to false and is
   preserved by configuration upgrades. Keep the normal log threshold at `info`;
   do **not** enable global debug/HTTP/body logging. The dedicated opt-in records
   are emitted at info level so unrelated verbose logging stays disabled.
3. In the same management room, using the same login as the failing commands,
   run `resolve-identifier ripharambe201669` once, then
   `search ripharambe201669` once. With multiple logins, explicitly select the
   same login using the framework's `[login ID]` argument. `resolve-identifier`
   executes the same lookup without creating a chat. Do not repeat `start-chat`
   merely to collect evidence: if resolution begins working it can create a room.
4. Export only the dedicated sanitized records. For a JSON-lines log file:

   ```sh
   jq -c 'select(.component == "lookup_diagnostic") |
     {component,lookup_trace,lookup_source,identifier_category,lookup}' bridge.log
   ```

   Export this output, the version metadata, UTC time and command outcome.
   Do not send the full log file, config, request/response body or contact list.
5. Set `network.lookup_diagnostics: false` and apply the configuration normally.

## Reading the evidence

`lookup_trace` is a fresh opaque request ID, not a hash of the identifier. Each
trace records the source (`search`, `resolve_identifier`, `start_chat`) and input
category. RPC records name the operation and show HTTP status, numeric API code
when available, JSON type/shape, collection counts and allowlisted schema keys.
MID-like fields show only byte length and shape state: `canonical`,
`valid_case_variant`, `empty`, `invalid_length`, `invalid_prefix`, `invalid_hex`.
These states describe syntax, not identity or LINE account existence.

Arbitrary field names can themselves contain identifiers, so unknown fields are
counted as `other_fields` rather than printed. Contact collection members are not
traversed. Profile values, messages, API error text, identifiers, tokens, cookies,
signatures, passwords and QR secrets are never added to these diagnostic records.
The observer is attached only to lookup contexts and allowlisted lookup RPCs;
authentication recovery RPCs do not receive response-shape instrumentation.

Connector records distinguish cache hit/miss/bypass, decoded collection sizes,
buddy/display-name fallback attempts, exact validation and final result count.
Use them to answer:

* Was the request actually made, or did a validated cache hit occur?
* Was there an HTTP error, nonzero API code, invalid JSON or decoder error?
* Was `data` empty/null, an array, or an object? Was `data.mid` present and typed
  as a string? Is an MID-shaped value only under `data.contact`/`data.result`?
* Which validation condition failed: malformed shape, requested MID mismatch or
  ambiguous contact-map keys? Did search then attempt display-name fallback?

If unknown field names or an undocumented restriction code prevent interpretation,
the next step is an operator-reviewed schema-only description or official protocol
evidence, with every value removed. Do not capture full response bodies by turning
on global debug logging. Alternate nesting probes in unit tests demonstrate
diagnostic visibility only; they do not justify accepting that schema for identity.

## Fix boundary and alternatives

No parser, endpoint, MID-format or visibility-policy change is justified until
these records identify the failing response and verified protocol evidence
establishes how to interpret it. A syntactically valid MID from a public-ID response
cannot alone prove that the remote service selected the intended person.

If LINE reports a documented lookup restriction, respect it. The supported bridge
alternative is an independently verified existing contact's internal user MID
(`start-chat <user MID>`), or an existing synchronized conversation. Confirm the
recipient before opening a chat. A display name is not a unique identity, and the
bridge cannot derive an internal MID from a public ID or override visibility rules.
