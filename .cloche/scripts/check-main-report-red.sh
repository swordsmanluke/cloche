#!/usr/bin/env bash
# check-main-report-red.sh — the suite failed on main's HEAD: post an urgent
# signal (source ci-main, key main-red) with the failing tests and the commit
# range since the last green check. It does not pause or stop anything.
#
# The failing tests come from the suite step's output (CLOCHE_PREV_OUTPUT).
# The signal goes to the daemon's intake endpoint with the project's token:
#   CLOCHE_SIGNAL_URL      default http://${CLOCHE_HTTP:-localhost:8080}
#   CLOCHE_SIGNAL_PROJECT  project name in the URL (default: project dir's basename)
# last_checked_sha is only recorded once the signal is accepted, so a failed
# post is retried on the next check.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/check-main-state.sh"

head=$(main_head)
green=$(state_get last_green_sha)

if [ -n "$green" ] && git -C "$PROJECT_DIR" cat-file -e "$green^{commit}" 2>/dev/null; then
  range="$green..$head"
  commits=$(git -C "$PROJECT_DIR" log --oneline "$range")
else
  range="(no earlier green check) ..$head"
  commits=$(git -C "$PROJECT_DIR" log --oneline -n 20 "$head")
fi

out=""
if [ -n "${CLOCHE_PREV_OUTPUT:-}" ] && [ -s "$CLOCHE_PREV_OUTPUT" ]; then
  out=$(cat "$CLOCHE_PREV_OUTPUT")
fi
failing=$(printf '%s\n' "$out" | grep -E '^(--- FAIL|FAIL[[:space:]])' | sort -u || true)
n=$(printf '%s\n' "$failing" | grep -c '^--- FAIL' || true)
if [ -z "$failing" ]; then
  failing="(no failing tests found in the suite output; it may not have built)"
  title="main is red: the suite fails at ${head:0:12}"
else
  title="main is red: $n failing test(s) at ${head:0:12}"
fi

evidence="Commit range since the last green check: $range
$commits

Failing tests:
$failing"

base="${CLOCHE_SIGNAL_URL:-}"
if [ -z "$base" ]; then
  addr="${CLOCHE_HTTP:-localhost:8080}"
  case "$addr" in :*) addr="localhost$addr" ;; esac
  case "$addr" in http*) base="$addr" ;; *) base="http://$addr" ;; esac
fi
project="${CLOCHE_SIGNAL_PROJECT:-$(basename "$(cd "$PROJECT_DIR" && pwd)")}"
token=$(cloche signals token --project "$PROJECT_DIR")

payload=$(TITLE="$title" EVIDENCE="$evidence" python3 -I -c '
import json, os
print(json.dumps({"source": "ci-main", "key": "main-red", "severity": "urgent",
                  "title": os.environ["TITLE"], "evidence": os.environ["EVIDENCE"][:60000]}))')

curl -sS -f -X POST "$base/api/projects/$project/signals" \
  -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
  -d "$payload"
echo
state_put last_checked_sha "$head"
echo "posted ci-main/main-red for ${head:0:12}"
