#!/usr/bin/env bash
set -euo pipefail
root=$(git rev-parse --show-toplevel)
cd "$root"
mode=${1:-normal}
if (($#)); then shift; fi
parallel=${TEST_PACKAGE_PARALLELISM:-2}
timeout=${TEST_TIMEOUT:-10m}
out=${TEST_OUTPUT_DIR:-"$root/.opencode-work/test-optimization/validation"}
mkdir -p "$out/tmp"
export TMPDIR="$out/tmp" GOTMPDIR="$out/tmp"
if [[ -z ${GOMAXPROCS:-} ]]; then
    cpus=$(getconf _NPROCESSORS_ONLN)
    export GOMAXPROCS=$((cpus < 4 ? cpus : 4))
fi
json=()
if [[ ${TEST_JSON:-0} == 1 ]]; then json=(-json); fi

format_check() {
    local files=()
    local listed
    listed=$(go list -f '{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}' "$@")
    while IFS= read -r file; do
        [[ -z "$file" ]] || files+=("$file")
    done <<< "$listed"
    local unformatted
    unformatted=$(gofmt -l "${files[@]}")
    if [[ -n "$unformatted" ]]; then
        printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
        return 1
    fi
}

lint() {
    local packages=()
    local listed
    listed=$(go list "$@")
    while IFS= read -r package; do
        [[ "$package" == */ltsm ]] || packages+=("$package")
    done <<< "$listed"
    if ((${#packages[@]})); then
        go vet "${packages[@]}"
        staticcheck "${packages[@]}"
    fi
}

normal() { go test -p "$parallel" -count=1 -timeout="$timeout" "${json[@]}" ./...; }
race() { go test -race -p "$parallel" -count=1 -timeout="$timeout" "${json[@]}" ./...; }
coverage() {
    go test -p "$parallel" -count=1 -timeout="$timeout" -covermode=atomic -coverprofile="$out/coverage.out" "${json[@]}" ./...
    python3 -B scripts/check_test_coverage.py "$out/coverage.out" >&2
}

case "$mode" in
    fast)
        if (($# == 0)); then
            printf 'Usage: bash scripts/test.sh fast ./changed/package [...]\n' >&2
            exit 2
        fi
        format_check "$@"
        lint "$@"
        run=()
        if [[ -n ${TEST_RUN:-} ]]; then run=(-run "$TEST_RUN"); fi
        go test -p "$parallel" -count=1 -timeout="$timeout" "${json[@]}" "${run[@]}" "$@"
        ;;
    normal) normal ;;
    race) race ;;
    coverage) coverage ;;
    release)
        format_check ./...
        lint ./...
        python3 -B -m unittest discover -s scripts -p test_check_test_coverage.py
        normal
        race
        coverage
        go build ./...
        GOFLAGS="${GOFLAGS:-} -o=$out/matrix-line" ./build.sh
        "$out/matrix-line" --version
        ;;
    *) printf 'Modes: fast, normal, race, coverage, release\n' >&2; exit 2 ;;
esac
