#!/bin/sh
set -eu
set +x

: "${LENS_ENROLLMENT_CODE:?inject a short-lived bootstrap code from secret tooling}"
: "${LENS_HUB_URL:?set the Lens Hub URL}"

bootstrap_code=$LENS_ENROLLMENT_CODE
hub_url=$LENS_HUB_URL
unset LENS_ENROLLMENT_CODE BARRIKADE_LENS_ENROLLMENT_CODE

cleanup() {
  bootstrap_code=
  unset bootstrap_code
}
trap cleanup EXIT HUP INT TERM

printf '%s\n' "$bootstrap_code" | barrikade-lens enroll \
  --enrollment-code-stdin \
  --hub "$hub_url" \
  --config /etc/barrikade-lens/config.json
systemctl enable --now barrikade-lens
