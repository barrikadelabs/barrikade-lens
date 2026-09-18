#!/bin/sh
set -eu

if command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now barrikade-lens || true
fi
