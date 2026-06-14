#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
run_id="${RELEASE_FUZZ_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
output_dir="${RELEASE_FUZZ_OUTPUT_DIR:-$repo_root/fuzz-report-$run_id}"
default_fuzztime="${RELEASE_FUZZTIME:-12h}"
timeout="${RELEASE_FUZZ_TIMEOUT:-0}"
required_targets=(FuzzRoundTrip FuzzDecompress FuzzCorruptStream FuzzOptions FuzzHeader)

mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
mkdir -p "$output_dir/logs"
summary="$output_dir/summary.csv"
metadata="$output_dir/metadata.txt"
crasher_manifest="$output_dir/crashers.csv"

discover_targets() {
  (cd "$repo_root" && go test -list '^Fuzz' | awk '/^Fuzz/ {print $1}')
}

targets=()
if [[ -n "${RELEASE_FUZZ_TARGETS:-}" ]]; then
  # shellcheck disable=SC2206
  targets=($RELEASE_FUZZ_TARGETS)
else
  targets=("${required_targets[@]}")
fi

if [[ "${#targets[@]}" -eq 0 ]]; then
  echo "no fuzz targets found" >&2
  exit 2
fi

discovered_targets=()
while IFS= read -r target; do
  discovered_targets+=("$target")
done < <(discover_targets)

for required in "${targets[@]}"; do
  found=0
  for discovered in "${discovered_targets[@]}"; do
    if [[ "$required" == "$discovered" ]]; then
      found=1
      break
    fi
  done
  if [[ "$found" -eq 0 ]]; then
    echo "missing fuzz target: $required" >&2
    exit 2
  fi
done

{
  printf "run_id=%s\n" "$run_id"
  printf "created_utc=%s\n" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf "repo_root=%s\n" "$repo_root"
  printf "output_dir=%s\n" "$output_dir"
  printf "go_version=%s\n" "$(go version)"
  printf "goos=%s\n" "$(go env GOOS)"
  printf "goarch=%s\n" "$(go env GOARCH)"
  printf "host=%s\n" "$(hostname)"
  printf "os=%s\n" "$(uname -srm)"
  printf "default_fuzztime=%s\n" "$default_fuzztime"
  printf "timeout=%s\n" "$timeout"
  printf "parallel=%s\n" "${RELEASE_FUZZ_PARALLEL:-}"
  printf "targets=%s\n" "${targets[*]}"
} > "$metadata"

printf "run_id,package,target,fuzztime,timeout,parallel,start_utc,end_utc,duration_seconds,result,exit_code,log_path,crasher_count,crasher_manifest_path\n" > "$summary"
printf "run_id,target,path\n" > "$crasher_manifest"

failures=0
for target in "${targets[@]}"; do
  fuzztime_var="FUZZTIME_${target}"
  fuzztime="${!fuzztime_var:-$default_fuzztime}"
  echo "running fuzz target $target for $fuzztime (timeout=$timeout)" >&2
  start_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  start_epoch="$(date +%s)"
  log_path="$output_dir/logs/${target}.log"
  before_crashers="$output_dir/logs/${target}.before_crashers"
  after_crashers="$output_dir/logs/${target}.after_crashers"
  find "$repo_root/testdata/fuzz/$target" -type f -print 2>/dev/null | sort > "$before_crashers" || true

  set +e
  if [[ -n "${RELEASE_FUZZ_PARALLEL:-}" ]]; then
    (cd "$repo_root" && go test -run=^$ -fuzz="^${target}$" -fuzztime="$fuzztime" -timeout="$timeout" -parallel "$RELEASE_FUZZ_PARALLEL" ./...) >"$log_path" 2>&1
  else
    (cd "$repo_root" && go test -run=^$ -fuzz="^${target}$" -fuzztime="$fuzztime" -timeout="$timeout" ./...) >"$log_path" 2>&1
  fi
  status=$?
  set -e

  find "$repo_root/testdata/fuzz/$target" -type f -print 2>/dev/null | sort > "$after_crashers" || true
  crasher_count=0
  while IFS= read -r crasher; do
    if [[ -n "$crasher" ]]; then
      crasher_count=$((crasher_count + 1))
      python3 - "$run_id" "$target" "$crasher" >> "$crasher_manifest" <<'PY'
import csv
import sys

writer = csv.writer(sys.stdout)
writer.writerow(sys.argv[1:])
PY
    fi
  done < <(comm -13 "$before_crashers" "$after_crashers")

  end_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  end_epoch="$(date +%s)"
  duration_seconds=$((end_epoch - start_epoch))
  result="pass"
  if [[ "$status" -ne 0 ]]; then
    result="fail"
    failures=$((failures + 1))
  fi

  python3 - "$run_id" "github.com/calorado/skanda-go" "$target" "$fuzztime" "$timeout" "${RELEASE_FUZZ_PARALLEL:-}" "$start_utc" "$end_utc" "$duration_seconds" "$result" "$status" "$log_path" "$crasher_count" "$crasher_manifest" >> "$summary" <<'PY'
import csv
import sys

writer = csv.writer(sys.stdout)
writer.writerow(sys.argv[1:])
PY

  if [[ "$result" == "fail" ]]; then
    echo "fuzz target failed: $target; log: $log_path" >&2
  fi
done

cat "$summary"

if [[ "$failures" -ne 0 ]]; then
  exit 1
fi
