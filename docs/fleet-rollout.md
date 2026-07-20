# Fleet rollout

The Hub Coverage page creates either a single-device ten-minute code or an endpoint-scoped fleet profile with an expiry and maximum use count. Treat either value as a bootstrap secret. Put it in Jamf, Intune, Fleet, or customer-managed secret tooling; never bake it into a pkg, MSI, deb, rpm, image, or Helm chart.

On first enrollment, the endpoint creates a per-Hub Ed25519 installation identity separately from its rotating collector credentials. The private identity file is protected with mode `0600` on macOS/Linux and a protected owner/SYSTEM/Administrators ACL on Windows. Service uninstall intentionally preserves both configuration and installation identity. Re-enrollment proves possession of that identity, reuses the target and active source, revokes old refresh credentials, and continues from the Hub's last accepted sequence.

## macOS

During the MVP, deploy the architecture-appropriate native binary from the verified GitHub release or npm package to `/usr/local/bin/barrikade-lens`, then run `packaging/macos/jamf-install.sh` with protected `LENS_ENROLLMENT_CODE` and `LENS_HUB_URL` variables. When run as root, `service install` creates a LaunchDaemon and the managed collector scans eligible profiles under `/Users`; when run as a user, it creates a private LaunchAgent for that profile. Apple-signed and notarized packages are deferred until production hardening.

## Windows

During the MVP, deploy the architecture-appropriate native binary from the verified GitHub release or npm package to `%ProgramFiles%\Barrikade Lens\barrikade-lens.exe`. Run `packaging/windows/intune-enroll.ps1` as an administrator with protected `BARRIKADE_LENS_ENROLLMENT_CODE` and `BARRIKADE_LENS_HUB` environment values. Enrollment writes the rotating collector credential under `%ProgramData%\Barrikade\Lens` with private permissions and installs the Windows service. The system service scans eligible profiles under `C:\Users` without reading secret values. Authenticode-signed MSI packages are deferred until production hardening.

## Linux

Deploy the deb or rpm, inject the profile values into `packaging/linux/fleet-install.sh`, and remove the bootstrap value from the deployment environment after enrollment. The systemd service scans eligible `/home` profiles. A non-root `barrikade-lens service install` remains available for a per-user collector.

Refresh credentials rotate one time at use, access tokens last 15 minutes, and source revocation invalidates refresh credentials immediately. Existing access tokens cannot ingest after revocation because Hub checks source state on every snapshot. Do not clone an enrolled identity file into a golden image: every endpoint must generate its own identity during rollout.
