#!/bin/sh
set -eu

purge_state=false
case "${1:-}" in
  "") ;;
  --purge-state) purge_state=true ;;
  *) echo "usage: $0 [--purge-state]" >&2; exit 64 ;;
esac

if [ "$(id -u)" -ne 0 ]; then
  echo "Run this cleanup as root." >&2
  exit 77
fi

configuration="/Library/Application Support/Barrikade/Lens/config.json"
if [ -x /usr/local/bin/barrikade-lens ]; then
  /usr/local/bin/barrikade-lens service uninstall --config "$configuration" || true
fi
rm -f /usr/local/bin/barrikade-lens
rm -rf /usr/local/share/barrikade-lens
pkgutil --forget ai.barrikade.lens >/dev/null 2>&1 || true

if [ "$purge_state" = true ]; then
  rm -rf "/Library/Application Support/Barrikade/Lens"
else
  echo "Barrikade Lens configuration and installation identity were preserved at /Library/Application Support/Barrikade/Lens."
fi
