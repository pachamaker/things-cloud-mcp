#!/usr/bin/env bash
# Takes one ADR-0005 conformance snapshot on this Mac (docs/testing/conformance.md):
# exports the Things app's views (export_things.py), dumps the server's mirror of
# the same sandbox account (TestSandboxConformanceDump), then compares the two
# (TestConformance_Snapshots). It only reads: the Things database through a
# read-only copy, Things Cloud through the read-only sandbox harness.
#
#   scripts/conformance/snapshot.sh <label>
#
# Writes testdata/conformance/<YYYY-MM-DDTHHMM>-<label>/{expected,input}.json.
# Needs THINGS_SANDBOX_ENV_FILE and the exporter's virtual environment
# (THINGS_CONFORMANCE_PYTHON, default ~/.venvs/things-conformance/bin/python).
set -euo pipefail

label=${1:-}
if [[ ! $label =~ ^[a-z0-9][a-z0-9-]{0,39}$ ]]; then
  echo "usage: $0 <label>  (lower-case letters, digits and hyphens, e.g. daytime, after-midnight, mac-closed)" >&2
  exit 2
fi
: "${THINGS_SANDBOX_ENV_FILE:?set THINGS_SANDBOX_ENV_FILE to the sandbox credentials file (docs/testing/sandbox-e2e.md)}"
python=${THINGS_CONFORMANCE_PYTHON:-$HOME/.venvs/things-conformance/bin/python}
if [[ ! -x $python ]]; then
  echo "no Python at $python: set up the exporter's virtual environment (docs/testing/conformance.md)" >&2
  exit 2
fi

repo=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
name="$(date +%Y-%m-%dT%H%M)-$label"
dir="$repo/testdata/conformance/$name"
if [[ -e $dir ]]; then
  echo "$dir exists" >&2
  exit 1
fi

# The title key lives outside the repository for the length of this run only:
# without it, committed title hashes cannot be checked against guessed titles.
key=$(mktemp "${TMPDIR:-/tmp}/things-conformance-key.XXXXXX")
trap 'rm -f "$key"' EXIT
chmod 600 "$key"
"$python" -I -c 'import secrets; print(secrets.token_hex(32))' >"$key"

mkdir -p "$dir"
discard() {
  rm -f "$dir/expected.json" "$dir/input.json"
  rmdir "$dir" 2>/dev/null || true
}
if ! "$python" -I "$repo/scripts/conformance/export_things.py" --out "$dir" --title-key-file "$key"; then
  discard
  exit 1
fi
zone=$("$python" -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["timezone"])' "$dir/expected.json")

cd "$repo"
if ! THINGS_TIMEZONE=$zone THINGS_CONFORMANCE_DIR=$dir THINGS_CONFORMANCE_TITLE_KEY_FILE=$key \
  go test -tags e2e -run '^TestSandboxConformanceDump$' -count=1 -v -timeout 20m ./server; then
  echo "the dump failed; the snapshot was discarded, take it again" >&2
  discard
  exit 1
fi

if go test -run "^TestConformance_Snapshots\$/^${name}\$" -count=1 -v ./server; then
  echo "snapshot $name: the server agrees with the Things app (known gaps aside)"
else
  echo "snapshot $name: mismatches above; each is a finding (docs/testing/conformance.md, \"Mismatches\")"
fi
echo "review $dir before committing it"
