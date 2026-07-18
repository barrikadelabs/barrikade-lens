# Fleet rollout

The Hub Sources page creates either a single-device ten-minute code or an endpoint-scoped fleet profile with an expiry and maximum use count. Treat either value as a bootstrap secret. Put it in Jamf, Intune, Fleet, or customer-managed secret tooling; never bake it into a pkg, MSI, deb, rpm, image, or Helm chart.

## macOS

Deploy the signed and notarized universal pkg, then run `packaging/macos/jamf-install.sh` with protected `LENS_ENROLLMENT_CODE` and `LENS_HUB_URL` variables. When run as root, `service install` creates a LaunchDaemon and the managed collector scans eligible profiles under `/Users`; when run as a user, it creates a private LaunchAgent for that profile.

## Windows

Deploy the signed architecture-appropriate MSI. Run `packaging/windows/intune-enroll.ps1` with protected `BARRIKADE_LENS_ENROLLMENT_CODE` and `BARRIKADE_LENS_HUB` environment values. Enrollment writes the rotating collector credential under `%ProgramData%\Barrikade\Lens` with private permissions and starts the Windows service. The system service scans eligible profiles under `C:\Users` without reading secret values.

## Linux

Deploy the deb or rpm, inject the profile values into `packaging/linux/fleet-install.sh`, and remove the bootstrap value from the deployment environment after enrollment. The systemd service scans eligible `/home` profiles. A non-root `barrikade-lens service install` remains available for a per-user collector.

Refresh credentials rotate one time at use, access tokens last 15 minutes, and source revocation invalidates refresh credentials immediately. Existing access tokens cannot ingest after revocation because Hub checks source state on every snapshot.
