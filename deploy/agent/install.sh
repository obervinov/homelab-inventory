#!/bin/sh
# Installs inventory-agent from the release bundle this script ships in:
# the binary, the systemd service and the timer.
#
#   install.sh [--config FILE] [--no-enable]
#
#   --config FILE   install FILE as /etc/homelab-inventory-agent/config.json
#   --no-enable     install only; do not enable and start the timer
#
# Safe to repeat, which is also how an upgrade is done. An existing config or token
# file is left in place, with its owner and mode brought back to root and 0600.
# The token is never written here. Put it in the file named by "token_file" in the
# config (root-owned, 0600) or pass it to the unit as INV_TOKEN.
#
# INSTALL_ROOT=<dir> stages the files under <dir> and skips systemd, for packaging
# and tests.
set -eu

usage() {
	sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
}

config=""
enable=1
while [ $# -gt 0 ]; do
	case "$1" in
	--config)
		if [ $# -lt 2 ]; then
			echo "install.sh: --config needs a file" >&2
			exit 2
		fi
		config="$2"
		shift 2
		;;
	--no-enable)
		enable=0
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
	esac
done

# CDPATH would make cd print the directory it resolved, putting two lines in $src.
unset CDPATH
src=$(cd -P -- "$(dirname -- "$0")" >/dev/null && pwd)
root="${INSTALL_ROOT:-}"
bin_dir="$root/usr/local/bin"
etc_dir="$root/etc/homelab-inventory-agent"
unit_dir="$root/etc/systemd/system"

if [ -z "$root" ] && [ "$(id -u)" -ne 0 ]; then
	echo "install.sh: run as root (or set INSTALL_ROOT to stage the files)" >&2
	exit 1
fi
if [ -n "$config" ] && [ ! -f "$config" ]; then
	echo "install.sh: config file not found: $config" >&2
	exit 1
fi
# Checked before anything is created: run from a source checkout there is no binary
# next to this script, and a half-made install is worse than none.
for f in inventory-agent inventory-agent.service inventory-agent.timer; do
	if [ ! -f "$src/$f" ]; then
		echo "install.sh: $src/$f is missing; run install.sh from an unpacked release bundle" >&2
		exit 1
	fi
done

# Ownership is set only when running as root; a staged install keeps the caller's.
own=""
if [ "$(id -u)" -eq 0 ]; then
	own="-o 0 -g 0"
fi

# The directory is 0700 and root-owned on purpose: the config names commands the agent
# runs as root, and the agent refuses a config or token that another user could change.
# shellcheck disable=SC2086 # $own is two words or nothing
{
	# mkdir, not install -d: these are system directories and keep whatever mode they have.
	mkdir -p "$bin_dir" "$unit_dir"
	install -d $own -m 0700 "$etc_dir"
	install $own -m 0755 "$src/inventory-agent" "$bin_dir/inventory-agent"
	install $own -m 0644 "$src/inventory-agent.service" "$unit_dir/inventory-agent.service"
	install $own -m 0644 "$src/inventory-agent.timer" "$unit_dir/inventory-agent.timer"
	if [ -n "$config" ]; then
		install $own -m 0600 "$config" "$etc_dir/config.json"
	fi
}
# Files from an earlier manual install: the agent refuses them with a looser mode.
for f in "$etc_dir/config.json" "$etc_dir/token"; do
	if [ -f "$f" ] && [ ! -L "$f" ]; then
		chmod 0600 "$f"
		if [ "$(id -u)" -eq 0 ]; then
			chown 0:0 "$f"
		fi
	fi
done

if [ -n "$root" ]; then
	echo "install.sh: staged under $root (INSTALL_ROOT is set); systemd was not touched"
	exit 0
fi

systemctl daemon-reload
if [ "$enable" -eq 1 ]; then
	systemctl enable --now inventory-agent.timer
fi
