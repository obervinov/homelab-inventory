#!/bin/sh
# Packs the host agent into one archive per architecture, so a host installs it from
# a single download checked against a single checksum:
#
#   inventory-agent_<version>_linux_<arch>.tar.gz
#     inventory-agent  inventory-agent.service  inventory-agent.timer
#     install.sh       config.example.json
#
#   bundle.sh VERSION DIST_DIR
#
# DIST_DIR must hold the built binaries (inventory-agent_<version>_linux_<arch>). The
# archives are written next to them and their checksums are added to DIST_DIR/SHA256SUMS.
set -eu

if [ $# -ne 2 ]; then
	echo "usage: bundle.sh VERSION DIST_DIR" >&2
	exit 2
fi
version="$1"
# CDPATH would make cd print the directory it resolved, putting two lines in the result.
unset CDPATH
dist=$(cd -P -- "$2" >/dev/null && pwd)
src=$(cd -P -- "$(dirname -- "$0")" >/dev/null && pwd)
# Modes inside the archive must not depend on the caller's umask.
umask 022

if command -v sha256sum >/dev/null 2>&1; then
	sum() { sha256sum "$@"; }
else
	sum() { shasum -a 256 "$@"; }
fi

# GNU tar can make the archive reproducible: fixed order, owner and time, so the same
# inputs give the same checksum. Other tars still produce a working archive.
tar_flags=""
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
	tar_flags="--sort=name --owner=0 --group=0 --numeric-owner --mtime=@${SOURCE_DATE_EPOCH:-0}"
fi

# macOS tar would add an AppleDouble "._" entry per file; no effect elsewhere.
export COPYFILE_DISABLE=1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

for arch in amd64 arm64; do
	name="inventory-agent_${version}_linux_${arch}"
	if [ ! -f "$dist/$name" ]; then
		echo "bundle.sh: missing binary $dist/$name" >&2
		exit 1
	fi
	mkdir "$work/$name"
	install -m 0755 "$dist/$name" "$work/$name/inventory-agent"
	install -m 0755 "$src/install.sh" "$work/$name/install.sh"
	install -m 0644 "$src/inventory-agent.service" "$src/inventory-agent.timer" "$src/config.example.json" "$work/$name/"
	# shellcheck disable=SC2086 # $tar_flags is a list of flags or nothing
	tar $tar_flags -C "$work" -cf - "$name" | gzip -n >"$dist/$name.tar.gz"
done

# Replace only this project's bundle lines; every other line stays as it was.
cd "$dist"
touch SHA256SUMS
awk '$2 !~ /^\*?inventory-agent_.*_linux_.*\.tar\.gz$/' SHA256SUMS >SHA256SUMS.new
sum inventory-agent_"${version}"_linux_*.tar.gz >>SHA256SUMS.new
mv SHA256SUMS.new SHA256SUMS
