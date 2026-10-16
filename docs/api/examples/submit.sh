#!/usr/bin/env bash
# Submit the generic sample document, poll until the job is terminal, print the result.
#
#   EXTRACTION_URL=http://127.0.0.1:8090 EXTRACTION_TOKEN=... docs/api/examples/submit.sh
#
# Optional: DOCUMENT (default examples/samples/booking.txt), RETENTION (none|default,
# default none), TIMEOUT seconds to wait (default 600). Requires curl and jq.
# Exit status: 0 accepted, 1 rejected or failed, 2 usage or transport error.
#
# Note: with RETENTION=none the first read of a terminal status deletes the job, so this
# script prints that read and does not poll again.
set -euo pipefail

: "${EXTRACTION_URL:?set EXTRACTION_URL, for example http://127.0.0.1:8090}"
: "${EXTRACTION_TOKEN:?set EXTRACTION_TOKEN (a token from vde token create)}"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
samples="$root/examples/samples"
document="${DOCUMENT:-$samples/booking.txt}"
retention="${RETENTION:-none}"
timeout="${TIMEOUT:-600}"
base="${EXTRACTION_URL%/}/v1"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

# The same document and instructions always give the same key, so rerunning this script
# after a lost response does not pay for a second extraction.
key="$(cat "$document" "$samples/extractor.md" "$samples/judge.md" "$samples/schema.json" |
  shasum -a 256 | cut -d' ' -f1)"

hdr="$(mktemp)"
trap 'rm -f "$hdr"' EXIT

echo "submitting $document" >&2
body="$(curl -sS -D "$hdr" "$base/jobs" \
  -H "Authorization: Bearer $EXTRACTION_TOKEN" \
  -H "Idempotency-Key: $key" \
  -F "document=@$document;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<$samples/extractor.md;type=text/markdown" \
  -F "judge_instructions=<$samples/judge.md;type=text/markdown" \
  -F "schema=<$samples/schema.json;type=application/schema+json" \
  -F "retention=$retention")" || exit 2

status_line="$(head -n1 "$hdr" | tr -d '\r')"
case "$status_line" in
  *" 202"*) ;;
  *)
    echo "submit failed: $status_line" >&2
    echo "$body" >&2
    grep -i '^\(retry-after\|x-quota-\)' "$hdr" >&2 || true
    exit 2
    ;;
esac

id="$(jq -r .id <<<"$body")"
echo "job $id queued (quota remaining: $(grep -i '^x-quota-remaining:' "$hdr" | tr -d '\r' | cut -d' ' -f2))" >&2

delay=2
waited=0
while :; do
  sleep "$delay"
  waited=$((waited + delay))
  if ! job="$(curl -sS --fail-with-body "$base/jobs/$id" -H "Authorization: Bearer $EXTRACTION_TOKEN")"; then
    echo "poll failed: $job" >&2
    exit 2
  fi
  status="$(jq -r .status <<<"$job")"
  echo "status: $status (waited ${waited}s)" >&2
  case "$status" in
    accepted | rejected | failed) break ;;
  esac
  if [ "$waited" -ge "$timeout" ]; then
    echo "gave up after ${waited}s; the job may still finish" >&2
    exit 2
  fi
  delay=$((delay * 2))
  [ "$delay" -le 30 ] || delay=30
done

jq . <<<"$job"
[ "$status" = accepted ]
