#!/usr/bin/env bash
# Scan the seeded instance with each token and compare every verdict against
# expected/<profile>.tsv. Exits 1 on any difference.
#
#   ./verify.sh                 every profile
#   ./verify.sh admin-full      one
#   BIN=/path/to/bitbucket-bench ./verify.sh
#
# Also asserts the property the tool is built on: the closing trace line must
# report zero writes, for every token.
set -euo pipefail
cd "$(dirname "$0")"

BIN=${BIN:-../../bin/bitbucket-bench}
OUT=out
# shellcheck disable=SC1091
. "$OUT/tokens.env"

python3 expected.py >/dev/null

# A case rather than an associative array: macOS still ships bash 3.2.
token_for() {
  case "$1" in
  admin-full) echo "$ADMIN_FULL_TOKEN" ;;
  admin-read) echo "$ADMIN_READ_TOKEN" ;;
  scanner) echo "$SCANNER_TOKEN" ;;
  *)
    echo "unknown profile $1" >&2
    exit 2
    ;;
  esac
}
profiles=("$@")
[ ${#profiles[@]} -eq 0 ] && profiles=(admin-full admin-read scanner)

failed=0
for profile in "${profiles[@]}"; do
  report=$OUT/$profile.report.json
  # A config file in the working directory or the user's config dir must not
  # leak into the comparison, so the scan runs against an empty one.
  printf 'scan:\n  cache: false\n' >"$OUT/empty.yaml"
  set +e
  "$BIN" scan --config "$OUT/empty.yaml" --url "$BITBUCKET_URL" --token "$(token_for "$profile")" \
    --snapshot-out "$OUT/$profile.snapshot.json" -o json --output-file "$report" \
    2>"$OUT/$profile.stderr"
  status=$?
  set -e
  if [ "$status" -eq 2 ]; then
    echo "[$profile] scan failed (exit 2):" >&2
    tail -5 "$OUT/$profile.stderr" >&2
    failed=1
    continue
  fi
  if ! grep -q ' 0 writes · read-only' "$OUT/$profile.stderr"; then
    echo "[$profile] the closing trace does not assert read-only:" >&2
    grep '✓\|✗' "$OUT/$profile.stderr" >&2 || true
    failed=1
  fi
  if ! python3 - "$profile" "$report" <<'EOF'; then failed=1; fi
import json, sys
profile, report = sys.argv[1], sys.argv[2]
want = {}
for line in open(f"expected/{profile}.tsv"):
    if line.startswith("#") or not line.strip():
        continue
    control, resource, status = line.rstrip("\n").split("\t")
    want[(control, resource)] = status
got, details = {}, {}
for f in json.load(open(report))["findings"]:
    key = (f["checkId"], f["resource"])
    got[key] = f["status"]
    details[key] = f.get("details", "")
bad = []
for key in sorted(set(want) | set(got)):
    w, g = want.get(key, "absent"), got.get(key, "absent")
    if w != g:
        bad.append((key, w, g))
if bad:
    print(f"[{profile}] {len(bad)} of {len(want)} verdicts differ:")
    for (control, resource), w, g in bad:
        print(f"  {control:<11} {resource:<22} want {w:<6} got {g:<6} {details.get((control, resource), '')[:110]}")
    sys.exit(1)
print(f"[{profile}] all {len(want)} verdicts as expected")
EOF
done
exit $failed
