#!/bin/sh
set -eu
set +x

LENS_HUB_URL=${LENS_HUB_URL:-${4:-}}
LENS_ENROLLMENT_CODE=${LENS_ENROLLMENT_CODE:-${5:-}}
: "${LENS_ENROLLMENT_CODE:?Provide LENS_ENROLLMENT_CODE through a protected Jamf parameter or environment value}"
: "${LENS_HUB_URL:?Provide LENS_HUB_URL through Jamf parameter 4 or an environment value}"

bootstrap_code=$LENS_ENROLLMENT_CODE
hub_url=$LENS_HUB_URL
unset LENS_ENROLLMENT_CODE BARRIKADE_LENS_ENROLLMENT_CODE

cleanup() {
  bootstrap_code=
  unset bootstrap_code
}
trap cleanup EXIT HUP INT TERM

printf '%s\n' "$bootstrap_code" | /usr/local/bin/barrikade-lens enroll \
  --enrollment-code-stdin \
  --hub "$hub_url" \
  --config "/Library/Application Support/Barrikade/Lens/config.json" \
  --install
