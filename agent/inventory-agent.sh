#!/bin/sh
# Reports this host's versions to homelab-inventory: OS, kernel, running
# container images and a chosen list of packages (installed + apt candidate).
# Needs only sh, curl and outbound HTTPS; run it from a systemd timer.
#
# Config (environment or /etc/homelab-inventory-agent.env):
#   INV_URL       https://<public host>/v1/report
#   INV_TOKEN     this host's report token
#   INV_PACKAGES  space-separated package names to track (optional)
set -eu

CONF="${INV_AGENT_CONF:-/etc/homelab-inventory-agent.env}"
# shellcheck disable=SC1090
[ -r "$CONF" ] && . "$CONF"
: "${INV_URL:?INV_URL is not set}"
: "${INV_TOKEN:?INV_TOKEN is not set}"

esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | tr -d '\000-\037'; }

ITEMS=""
add() { # kind name version [available]
  [ -n "$2" ] && [ -n "$3" ] || return 0
  item="{\"kind\":\"$1\",\"name\":\"$(esc "$2")\",\"version\":\"$(esc "$3")\""
  [ -n "${4:-}" ] && [ "$4" != "$3" ] && item="$item,\"available\":\"$(esc "$4")\""
  ITEMS="${ITEMS:+$ITEMS,}$item}"
}

if [ -r /etc/os-release ]; then
  add os "$(. /etc/os-release && printf '%s' "${ID:-linux}")" "$(. /etc/os-release && printf '%s' "${VERSION_ID:-unknown}")"
fi
add kernel linux "$(uname -r)"

if command -v docker >/dev/null 2>&1; then
  for ref in $(docker ps --format '{{.Image}}' 2>/dev/null | sort -u); do
    ref="${ref%%@*}"
    last="${ref##*/}"
    case "$last" in
      *:*) add docker "${ref%:*}" "${ref##*:}" ;;
      *)   add docker "$ref" latest ;;
    esac
  done
fi

if command -v dpkg-query >/dev/null 2>&1; then
  for pkg in ${INV_PACKAGES:-}; do
    cur="$(dpkg-query -W -f='${Version}' "$pkg" 2>/dev/null || true)"
    cand="$(apt-cache policy "$pkg" 2>/dev/null | awk '/Candidate:/{print $2}')"
    add package "$pkg" "$cur" "$cand"
  done
fi

BODY="{\"items\":[$ITEMS]}"
if [ "${INV_DRY_RUN:-}" = "1" ]; then
  printf '%s\n' "$BODY"
  exit 0
fi
curl --fail --silent --show-error --max-time 30 --retry 3 \
  -H "Authorization: Bearer $INV_TOKEN" -H 'Content-Type: application/json' \
  --data "$BODY" "$INV_URL"
