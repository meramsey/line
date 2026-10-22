# matrix-line release artifacts

## matrix-line-linux-amd64

- Version: `v1.2.0+dev.8e54b46d`
- Commit: `8e54b46d59c7298d4c194329435f150a7edbaaf2`
- Branch: `integration/linux-build-20261020`
- Built: `2026-10-22T17:07:10Z`
- Go: `go1.26.10`
- Mautrix: `v0.31.0` (`34107524d2ab`)
- Architecture: `linux/amd64`
- Dependencies: dynamically linked to `libolm.so.3`
- Build: reproducible with `SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)`

## Files

| File | SHA256 |
|------|--------|
| `matrix-line-linux-amd64` | `8993bb61a9f11e2d66e5d61b4de8d5139b929ffea4311a05f876765b72d290a6` |
| `matrix-line-linux-amd64.tar.gz` | `b815627ec2c117009d6db8f8b52d038756c1f2a8cd4282d83729efd34ef92649` |

## Verification

```bash
sha256sum -c SHA256SUMS
./releases/matrix-line-linux-amd64 --version
./releases/matrix-line-linux-amd64 --version-json
file releases/matrix-line-linux-amd64
ldd releases/matrix-line-linux-amd64 | grep olm
```

## Changes in this release

- Structured authentication errors (`AuthError`) normalizing LINE provider exceptions.
- Safe provider-alert/reason handling with no raw text leakage.
- Extended cooldown handling for verification-temporarily-unavailable errors.
- Structured logging fields for QR login failures.
- Reproducible build support via `SOURCE_DATE_EPOCH`.
