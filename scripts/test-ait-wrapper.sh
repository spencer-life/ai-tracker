#!/bin/sh
set -eu

repo_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
wrapper="$repo_root/scripts/ait"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

sh -n "$wrapper"
cp "$wrapper" "$tmp_dir/ait"

# The single quotes intentionally preserve variables for the generated fixture.
# shellcheck disable=SC2016
printf '%s\n' \
	'#!/bin/sh' \
	'printf "%s\n" "$@" > "$AIT_TEST_OUTPUT"' \
	'exit "${AIT_TEST_EXIT:-0}"' >"$tmp_dir/ai-tracker"
chmod 0755 "$tmp_dir/ait" "$tmp_dir/ai-tracker"

AIT_TEST_OUTPUT="$tmp_dir/actual" "$tmp_dir/ait" alpha "two words" --flag=value
printf '%s\n' alpha "two words" --flag=value >"$tmp_dir/expected"
cmp "$tmp_dir/expected" "$tmp_dir/actual"

set +e
AIT_TEST_OUTPUT="$tmp_dir/exit-args" AIT_TEST_EXIT=23 "$tmp_dir/ait" exit-check
status=$?
set -e

if [ "$status" -ne 23 ]; then
	echo "ait wrapper returned $status; expected 23" >&2
	exit 1
fi

printf '%s\n' exit-check >"$tmp_dir/exit-expected"
cmp "$tmp_dir/exit-expected" "$tmp_dir/exit-args"

mkdir "$tmp_dir/wrapper-only"
cp "$wrapper" "$tmp_dir/wrapper-only/ait"
AIT_TEST_OUTPUT="$tmp_dir/path-actual" PATH="$tmp_dir:$PATH" \
	"$tmp_dir/wrapper-only/ait" path-fallback
printf '%s\n' path-fallback >"$tmp_dir/path-expected"
cmp "$tmp_dir/path-expected" "$tmp_dir/path-actual"
