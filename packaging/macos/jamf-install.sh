#!/bin/sh
set -eu
: "${LENS_ENROLLMENT_CODE:?Provide LENS_ENROLLMENT_CODE through a protected Jamf parameter}"
: "${LENS_HUB_URL:?Provide LENS_HUB_URL through a Jamf parameter}"
/usr/local/bin/barrikade-lens enroll "$LENS_ENROLLMENT_CODE" --hub "$LENS_HUB_URL" --config "/Library/Application Support/Barrikade/Lens/config.json" --install
