# Test performance and validation

## Scope and reproducible baseline

Baseline: `587164f7ced99bc5765ac7df4eafc11314e6d602` (PR #271).
Measured optimization revision: `a8d80f22c3ce1e18470f0c7d8c10e5cd8ad6f6ee`.
The documentation commit adds this report without changing the measured Go code.
Work is on `optimization/test-performance-20261020`; the integration branch,
published PR history and release artifacts are preserved. This branch was not
pushed to PR #271.

The baseline was measured before editing tests. Normal, race and coverage runs
independently reproduced **382 passing tests/subtests, zero failures and one
pre-existing skip**. The skipped `TestReplayLTSMVectors` remains an upstream stub
pending a rewrite of its old JSON-IPC replay path. Three packages have no tests.

## Measurement controls

* Linux amd64, Go 1.26.10, GCC 13.3.0, CGO enabled, libolm 3.2.16.
* Host: 16 logical CPUs, approximately 62 GiB RAM. Both revisions use
  `GOMAXPROCS=4` and package parallelism `-p 2` for the main comparison.
* Module downloads were already cached. **Cold** means an empty, isolated Go
  build cache, not a reboot or a flushed OS filesystem/module-download cache.
* Every measured test execution uses `-count=1`, or an explicit repetition count.
  Cached test results never substitute for executed tests.
* Phases run serially. Raw Go JSON, `/usr/bin/time -v`, CPU/heap profiles,
  environment, commands, source revision and load averages are retained.
* The driver samples process-tree RSS every 100 ms. This sums shared pages and
  can miss short peaks; GNU time additionally records the kernel's maximum
  single-process RSS. Neither is represented as exact total physical memory.
  Aggregate CPU utilization may exceed 100%.
* Baseline vector tests wrote fixed `/tmp` files. For both revisions, bubblewrap
  redirects `/tmp` to project-local scratch storage, without editing the baseline.
* Cold compile-only, warm compile-only, initialization-only and cold end-to-end
  phases are distinct measurements. Do not add compile-only time to execution
  time: compilation and execution can overlap in a full `go test` run.

The initial sandbox probe failed before tests ran because `/dev/null` was not
available. It was corrected using a private `/dev` mount; those failed probe
results remain in `baseline/` and are excluded from comparisons. An interrupted
optimized-driver launch is also retained separately. Valid comparisons use only
`baseline-measured/` and `optimized-final/`.

## Results

### End-to-end test commands and compilation

Positive percentages indicate an improvement; negative values indicate a slowdown.

| Measurement | Before | After | Improvement |
| --- | ---: | ---: | ---: |
| Full normal, warm build cache, uncached tests | 56.709 s | 6.096 s | **89.25%** |
| Full race, warm build cache, uncached tests | 1403.551 s | 146.217 s | **89.58%** |
| Full normal, cold build cache | 384.700 s | 393.692 s | **-2.34%** |
| Full race, cold build cache | 1594.938 s | 547.964 s | **65.64%** |
| Normal compile-only, cold cache | 442.656 s | 391.731 s | 11.50% |
| Normal compile-only, warm cache | 0.227 s | 0.242 s | -6.33% |
| Race compile-only, cold cache | 489.540 s | 494.458 s | -1.00% |
| Race compile-only, warm cache | 0.340 s | 0.240 s | 29.52% |
| Coverage compile-only, cold cache | 378.120 s | 381.373 s | -0.86% |
| Full atomic-coverage run, warm build cache | 52.542 s | 12.782 s | 75.67% |
| Initialization/no test bodies, normal | 0.904 s | 0.930 s | -2.88% |
| Initialization/no test bodies, race | 4.511 s | 4.377 s | 2.98% |

The 30% target and 50% stretch goal were exceeded for uncached **test execution**
with a warm build cache, and for cold-cache race validation. Cold-cache normal
validation did not improve: the 431k-line generated Go implementation still
dominates compilation. Compiler timings vary across runs; the generated source
was not edited, and the compile-only difference is not claimed as a direct
algorithmic improvement from the fixture change. Small differences in subsecond
phases should not be overinterpreted.

### CPU and memory

| Full-suite mode | CPU seconds before → after | CPU reduction | Peak process-tree RSS before → after |
| --- | ---: | ---: | ---: |
| Normal, warm build cache | 61.561 → 10.414 | **83.08%** | 414.0 → 411.2 MiB |
| Race, warm build cache | 1464.507 → 199.452 | **86.38%** | 532.7 → 534.6 MiB |
| Normal, cold build cache | 1395.255 → 1378.012 | 1.24% | 8140.3 → 8395.2 MiB |
| Race, cold build cache | 2813.127 → 1632.043 | 41.98% | 6575.9 → 6399.6 MiB |

Warm-run CPU utilization was 108.6% → 170.8% normally and 104.3% → 136.4%
under race: bounded parallel work occupies a larger fraction of the much shorter
run, while total CPU consumed falls sharply. Peak RAM is effectively unchanged;
this work does not claim a substantial memory reduction. Cold generated-code
compilation remains the largest memory consumer (approximately 8–9 GiB peak tree
RSS for some compile/coverage phases).

## Bottleneck ranking

### Packages (execution times from Go JSON)

| Package suffix | Normal before → after | Race before → after |
| --- | ---: | ---: |
| `pkg` | 56.319 → 5.812 s | 1402.986 → 145.797 s |
| `pkg/e2ee` | 1.415 → 1.335 s | 37.158 → 35.041 s |
| `pkg/connector` | 0.876 → 0.837 s | 9.952 → 8.736 s |
| `pkg/ltsm` | 0.509 → 0.461 s | 9.144 → 8.806 s |
| `pkg/line` | 0.118 → 0.109 s | 2.708 → 2.775 s |
| `pkg/connector/handlers` | 0.107 → 0.006 s | 1.125 → 1.025 s |

Package times overlap and must not be summed to obtain suite wall time.

### Individual tests

| Test | Normal before → after | Race before → after |
| --- | ---: | ---: |
| `TestGetSignatureRecoversFromExhaustedRuntime` | 50.52 → 0.43 s | 1258.74 → 9.64 s |
| `TestGetSignatureReusesSigningContext` | 5.29 → 4.91 s | 134.28 → 126.70 s |
| `TestV1WireHeaderUsesV1DecryptWithoutMetadata` | 1.41 → 1.33 s | 36.14 → 34.03 s |
| `TestGetSignatureEvictsSigningContexts` | 0.19 → 0.18 s | 3.73 → 3.54 s |

`TestGetLastOpRevisionContextCancellation` took 1.11 s in the baseline race run
(normal timing rounded below Go JSON's displayed precision). It and the eviction
test passed ten shuffled race repetitions each before and after optimization;
those two-case runs took 37.126 s and 37.203 s respectively. They are not the
current major bottleneck and their timing/cancellation assertions were preserved.

CPU profiling attributed **90.68%** of signing-test CPU samples to
`SecureKeyLoadToken`, primarily generated byte loads/stores during heap setup.
This was cryptographic computation, not network waiting or package initialization.

## Optimizations and behavioral invariants

### Native heap-pressure fixture

`AllocMemory`/`FreeMemory` expose the same allocator already used internally by
LTSM, alongside `ModuleMem`. The exhaustion test reserves real 64 KiB native
allocations, then executes the original real key-loading loop until a real
`ltsm.ErrAbort`. In the measured runtime it reserved 8,323,072 bytes before the
final key-allocation phase. Neither the allocator nor cryptography is mocked;
no memory offsets, generated instructions, default heap sizes or synthetic abort
flags are patched.

Every original regression condition remains:

1. Initial signing succeeds.
2. Real native key creation aborts on the fixed heap, within the existing
   20,000-iteration safety bound.
3. Signing with another token recovers successfully.
4. The aborted signing runtime is replaced.
5. The original signature is unchanged after recovery.
6. E2EE memory is byte-for-byte unchanged.

The original 6,000-signature reuse loop and 1,800-decryption loop remain intact.
Two added tests check allocation isolation and native exhaustion/free/coalescing/
reuse. An alternative experiment importing an exported immutable key fixture was
only about twice as fast as token decoding; explicit real allocator pressure
better expresses the resource condition without redundant cryptographic setup.

### Deterministic waiting and safe fixture isolation

The album-preview concurrency test uses `testing/synctest` quiescence before
asserting that a fifth worker cannot start while four slots are occupied. This
replaces a fixed 100-ms sleep-like wait with a deterministic scheduling condition;
it cannot pass simply because an incorrectly admitted worker was not scheduled
soon enough. Worker-limit, completion-order and output assertions remain, as do
bounded timeout guards.

Vector generators still perform real cryptography and retain JSON in test output,
but write files through `t.TempDir()` and fail on write errors. Their two genuinely
independent cases now use `t.Parallel()`. Tests mutating client factories,
environment variables or stdout remain sequential. Database fixtures remain
isolated; measured database setup cost did not justify mutable shared databases.

Most `time.After` calls are failure bounds, not delays on successful runs; these
were not shortened. The remaining forced-logout 100-ms negative assertion depends
on mutex blocking and was not replaced with an unsafe virtual-clock assumption.
No busy-wait replacement was introduced.

## Coverage and regression preservation

Equivalent command scope: `go test -covermode=atomic -count=1 -p 2 ./...`.

| Scope | Covered / total before | Covered / total after | Coverage before → after |
| --- | ---: | ---: | ---: |
| All statements | 174033 / 412957 | 174101 / 412959 | **42.143% → 42.159%** |
| Non-generated statements | 3288 / 9302 | 3293 / 9304 | **35.347% → 35.393%** |
| `pkg/ltsm`, all | 171289 / 404716 | 171357 / 404718 | 42.323% → 42.340% |
| `pkg/ltsm`, non-generated | 544 / 1061 | 549 / 1063 | 51.272% → 51.646% |

Every other package's covered/total statement counts were unchanged: root
235/736, connector 1528/4409, handlers 368/985, e2ee 218/596, line 395/1479;
main, password and secret remain 0/3, 0/26 and 0/7 respectively.

The raw block comparison found **zero previously covered blocks lost**, including
generated blocks. Existing production source positions remain comparable because
the allocator adapter is a separate file and generated code is untouched. This
block comparison and the explicit invariant audit complement the percentages.
The full profile includes generated code; the non-generated view prevents its
400k+ statements from hiding a handwritten-code regression.

Go test/subtest count increased **382 → 384**, with zero failures and the same one
pre-existing skip per full run. No existing test or regression scenario was removed.

## Parallelism and stability

`-p 1` was a prior diagnostic/resource workaround, not a requirement arising from
cross-package state. Package processes isolate their globals; baseline and
optimized full runs passed with bounded `-p 2`.

On the optimized code, additional warm-cache `-p 1` runs took **10.30 s normal**
and **201.18 s race**, versus the main `-p 2` measurements of 6.096 s and 146.217 s.
The serial control used GNU time directly, whereas the main measurements include
the small sandbox/sampling overhead. Keep `TEST_PACKAGE_PARALLELISM=1` available
for memory-constrained machines, rather than forcing all race execution serial.

Stability validation:

* Baseline: two full normal repetitions with seed 271, 764 passes / 0 failures;
  ten race repetitions of each previously problematic test, 20 passes / 0 failures.
* Optimized: two equivalent normal repetitions, 768 passes / 0 failures; the same
  targeted ten-repeat race check, 20 passes / 0 failures.
* Additional optimized normal runs: seeds 101, 90210 and 20261020, three full
  repetitions each: **3456 passes, 0 failures**, nine expected skips.
* Additional optimized full race runs: seeds 101 and 90210:
  **768 passes, 0 failures**, two expected skips.
* The full release validation script passed formatting, vet, staticcheck, seven
  coverage-gate unit tests, normal tests, race tests, coverage gating and a native
  build/version smoke check. All nine project pre-commit checks and actionlint
  passed. No flakiness was observed in these runs; this is not a guarantee against
  all possible schedules.

## Developer and release validation levels

Requirements: Go 1.26+, CGO/libolm, goimports v0.52.0 and staticcheck v0.8.2.
The retained workspace's `.opencode-work/env.sh` supplies its local toolchain and
library paths without changing system-wide Go settings.

```bash
source .opencode-work/env.sh
TEST_RUN='TestResolveIdentifier' bash scripts/test.sh fast ./pkg/connector
bash scripts/test.sh normal
bash scripts/test.sh race
bash scripts/test.sh coverage
bash scripts/test.sh release
```

Fast mode requires explicit packages and supports `TEST_RUN`. Full modes ignore
that selector and include all tests. Package parallelism defaults to 2;
`GOMAXPROCS` defaults to at most 4. The optimized scripts use the standard
10-minute package timeout; benchmarks use 60 minutes on both revisions for a
comparable baseline. `-count=1` prevents test-result caching. All scratch files,
profiles and the release-validation binary stay under
`.opencode-work/test-optimization/`; existing `dist/` artifacts are untouched.

## CI changes and repository configuration

The original workflow ran lint/build/Docker jobs, but no Go tests, race suite or
coverage gate. The new workflow adds **Test (normal)** and **Test (race)**. Add
these names to required branch-protection checks when adopting it. Existing
`Lint with 1.26`, `Build 1.26` and `build-docker` names remain. Repository branch
protection settings were not changed by this work.

* Normal tests and atomic coverage run together once; the complete race suite
  runs separately. Both are mandatory deployment dependencies alongside existing
  lint/build jobs. Vet/staticcheck retain only the existing generated-package
  exception; race testing excludes no package.
* `.github/coverage-baseline.tsv` records the measured package floors. Missing
  packages, fewer covered statements, or lower covered fractions fail, both for
  all code and non-generated code. Aggregate floors also prevent new uncovered
  packages from diluting coverage while older packages still pass. Intentional
  floor changes require review; there is no automatic lowering. Eight Python
  tests exercise pass/fail behavior; the eighth specifically checks new-package
  dilution and was validated after the release script run.
* Module and compiled-test caches are separate. Compiled cache keys include Go
  version, native ABI, instrumentation mode, module versions and source/data
  hashes; bump the cache schema if instrumentation flags change. Lint binaries
  and static analysis use pinned-version caches.
* Stale PR runs are canceled; main publication is not canceled mid-flight.
  Push workflows are limited to main, avoiding feature-branch push + PR duplicates.
* Docker uses GHA layer caching. Fork PRs build without registry secrets or image
  publication. Upstream main retains authenticated publication and deployment.
  BuildKit cache mounts are not fully exported by layer caching, so changed-source
  Docker builds may still recompile. Native glibc binaries are not reused in
  Alpine/musl images because of CGO ABI incompatibility.

Historical run `38662321323` took **365 s** end-to-end: lint 62 s, native build
31 s, Docker build 337 s, deployment 21 s (actual Docker build step: 319 s).
It did not include Go tests/race/coverage, so it is not an equivalent full-validation
baseline. PR #271's run required maintainer approval. **No post-change GitHub wall
time is claimed:** this branch has not been pushed and #271 was not modified.
Workflow syntax/action expressions and the validation commands were checked locally.

## Reproduce the measurements

The Linux driver requires `/usr/bin/time`, `bwrap`, Python 3 and `/proc`.
It records per-package/test durations, CPU, maximum process-tree RSS, GNU time
reports, load averages and CPU/heap profiles. It refuses dirty sources and cannot
silently resume failed or source-mismatched results. Use fresh labels for new
cold experiments; prior result files are never overwritten.

```bash
source .opencode-work/env.sh
python3 -B scripts/measure_tests.py new-after --parallel 2 --gomaxprocs 4
```

For the baseline, create a detached worktree inside project scratch space rather
than resetting the integration branch. Invoke the measurement script from the
optimization checkout. Ensure `.opencode-work/` is ignored in that worktree; the
retained repository's local excludes already provide this.

```bash
root=$PWD
git worktree add --detach "$root/.opencode-work/test-optimization/repro-baseline" \
  587164f7ced99bc5765ac7df4eafc11314e6d602
python3 -B "$root/scripts/measure_tests.py" new-before \
  --repo "$root/.opencode-work/test-optimization/repro-baseline" \
  --parallel 2 --gomaxprocs 4
```

Important phase commands (the driver supplies separate cache/output paths):

```bash
go test -p 2 -c -o BINARY_DIRECTORY ./...
go test -race -p 2 -c -o RACE_BINARY_DIRECTORY ./...
go test -p 2 -count=1 -timeout=60m -json ./...
go test -race -p 2 -count=1 -timeout=60m -json ./...
go test -p 2 -covermode=atomic -coverprofile=coverage.out -count=1 -json ./...
go test -p 2 -count=3 -shuffle=101 -json ./...
go test -race -p 2 -count=1 -shuffle=90210 -timeout=10m -json ./...
```

Raw results are under
`.opencode-work/test-optimization/{baseline-measured,optimized-final}/`, with
additional serial/shuffle/release-validation logs alongside them. Every phase's
exact command and cache path is recorded in its summary; environment and source
revision are in `environment.json`. CPU/heap profiles use the precompiled root
test binary and `-test.cpuprofile` / `-test.memprofile`. An initial `loadToken`
versus exported-key `loadKey` experiment is retained as well. Raw results, caches
and binaries are intentionally not committed.
