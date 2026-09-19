#!/bin/sh
set -eu

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  if [ -f /etc/barrikade-lens/config.json ]; then
    systemctl enable --now barrikade-lens
  fi
fi
