#!/bin/sh
set -eu
: "${LENS_ENROLLMENT_CODE:?inject a short-lived bootstrap code from secret tooling}"
: "${LENS_HUB_URL:?set the Lens Hub URL}"
barrikade-lens enroll "$LENS_ENROLLMENT_CODE" --hub "$LENS_HUB_URL" --config /etc/barrikade-lens/config.json
systemctl enable --now barrikade-lens
