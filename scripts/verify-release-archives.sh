#!/bin/sh
set -eu

dist_dir=${1:-dist}
tmp_root=$(mktemp -d)
trap 'rm -rf "$tmp_root"' EXIT HUP INT TERM
found_archive=false
found_linux_x86_64=false

for archive in "$dist_dir"/*.tar.gz; do
	[ -f "$archive" ] || continue
	found_archive=true
	archive_name=$(basename "$archive" .tar.gz)
	extract_dir="$tmp_root/$archive_name"
	mkdir -p "$extract_dir"
	tar -xzf "$archive" -C "$extract_dir"

	test -x "$extract_dir/ait"
	test -x "$extract_dir/ai-tracker"

	case "$archive_name" in
	*Linux_x86_64)
		found_linux_x86_64=true
		"$extract_dir/ait" version
		"$extract_dir/ait" --version
		"$extract_dir/ait" dashboard --help >/dev/null
		"$extract_dir/ai-tracker" version
		;;
	esac
done

if [ "$found_archive" != true ]; then
	echo "no release archives found under $dist_dir" >&2
	exit 1
fi

if [ "$found_linux_x86_64" != true ]; then
	echo "Linux x86-64 release archive not found under $dist_dir" >&2
	exit 1
fi
