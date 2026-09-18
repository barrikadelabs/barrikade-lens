# Fleet rollout

Use the signed native package for managed fleets. Node.js is not required. The Hub Coverage page creates an endpoint-scoped fleet profile with an expiry and maximum use count; every code created from that profile is a bootstrap secret. Inject it only at deployment time. Never put it in a pkg, MSI, deb, rpm, golden image, response file, or command-line log.

The package contains no tenant data. On first enrollment, Lens creates a different Ed25519 installation identity for each machine, exchanges the bootstrap code for that machine's rotating collector credentials, uploads an initial snapshot, and then starts the service. A failed attempt returns a nonsecret exit status and can be retried with a fresh code. The retained installation identity proves that a retry is the same endpoint, so it does not create a duplicate target.

The supported package baselines are:

| Platform | Package | Architectures | Supported baseline |
| --- | --- | --- | --- |
| macOS | notarized universal pkg | Intel and Apple silicon | macOS 13 or newer |
| Windows | MSI | x64 and ARM64 | Windows 10 22H2, Windows 11, or Windows Server 2022 or newer |
| Debian family | deb | amd64 and arm64 | Ubuntu 22.04 or newer; Debian 12 or newer |
| RHEL family | rpm | x86_64 and aarch64 | RHEL or Rocky Linux 9 or newer |

Verify the release checksum and signature before importing an artifact into device management. Verification commands and provenance details are in [releasing.md](releasing.md).

## macOS with Jamf

1. Upload `barrikade-lens-universal.pkg` from the GitHub release to Jamf Pro and add it to a computer policy. The package installs `/usr/local/bin/barrikade-lens`, license material, a `0700` state directory, and the cleanup helper.
2. Add `packaging/macos/jamf-install.sh` after the package in the same policy. Label script parameter 4 `Lens Hub URL` and parameter 5 `Short-lived Lens bootstrap credential`. Use a tightly scoped profile with a short expiry and maximum use count. Do not enable shell tracing or echo the parameter.
3. Run the policy as root. The script clears its environment copy, sends the credential to Lens over standard input, and calls `enroll --install`. The native CLI writes configuration and identity with private permissions, performs the first upload, stages the managed binary, and creates the system LaunchDaemon.
4. Use `/usr/local/bin/barrikade-lens service status --config "/Library/Application Support/Barrikade/Lens/config.json"` as the policy verification command.

For a retry, generate a fresh code and rerun only the script. For an upgrade, deploy the newer pkg. Its postinstall hook restages and restarts an already-enrolled service without replacing configuration or identity; no bootstrap code is needed.

Normal removal preserves local state so the same endpoint can be reinstalled:

```sh
sudo /usr/local/share/barrikade-lens/full-cleanup.sh
```

For an intentional decommission, revoke the source in Hub first, save the helper outside its installed directory if required by the MDM, and remove all local identity and configuration:

```sh
sudo /usr/local/share/barrikade-lens/full-cleanup.sh --purge-state
```

## Windows with Intune

1. Add the x64 and ARM64 MSI files to separate Intune Win32 app packages and apply architecture requirements. Use a silent install command such as `msiexec.exe /i barrikade-lens-win32-x64.msi /qn /norestart`. Detection can use the MSI product plus `%ProgramFiles%\Barrikade Lens\barrikade-lens.exe`.
2. Run `packaging/windows/intune-enroll.ps1` as `SYSTEM` after the MSI installation. Supply `BARRIKADE_LENS_HUB` and `BARRIKADE_LENS_ENROLLMENT_CODE` from the deployment system's protected variables; do not place the code in the `.intunewin` payload or install command. `-MsiPath` may be supplied when the package and enrollment script are run together.
3. The script removes the credential from its environment before launching Lens, protects `%ProgramData%\Barrikade\Lens` for SYSTEM and Administrators only, redirects stdin, and reports only a nonsecret exit code on failure. Lens completes enrollment before starting the Windows service.
4. Verify with `Get-Service BarrikadeLens` and `& "$env:ProgramFiles\Barrikade Lens\barrikade-lens.exe" service status --config "$env:ProgramData\Barrikade\Lens\config.json"`.

Retry the enrollment script with a fresh code after correcting network, clock, or policy issues. Installing a newer MSI preserves `%ProgramData%\Barrikade\Lens` and starts the existing configuration with the new signed executable.

Standard uninstall is silent and preserves identity/configuration:

```powershell
msiexec.exe /x barrikade-lens-win32-x64.msi /qn /norestart
```

For an intentional decommission, revoke the source in Hub and run the installed full-cleanup helper from an elevated PowerShell session. It uninstalls the MSI and permanently deletes the exact Lens state directory:

```powershell
& "$env:ProgramFiles\Barrikade Lens\full-cleanup.ps1" -Confirm:$false
```

## Linux

Install deb and rpm packages non-interactively, then inject protected variables into the packaged fleet script:

```sh
sudo env DEBIAN_FRONTEND=noninteractive dpkg -i barrikade-lens_VERSION_linux_amd64.deb
sudo --preserve-env=LENS_HUB_URL,LENS_ENROLLMENT_CODE /usr/share/barrikade-lens/fleet-install.sh
```

For rpm systems, use `dnf install -y ./barrikade-lens_VERSION_linux_amd64.rpm` and run the same script. The script clears its copy of the bootstrap credential and sends it through stdin. Configuration is stored under `/etc/barrikade-lens` with mode `0700`; runtime state uses `/var/lib/barrikade-lens`. Package upgrades restart an enrolled service and preserve both directories.

`apt remove barrikade-lens`, `dpkg -r barrikade-lens`, or `rpm -e barrikade-lens` stops the service and preserves nonempty state. For a full decommission, revoke the source, remove the package, and explicitly remove only the Lens directories:

```sh
sudo rm -rf /etc/barrikade-lens /var/lib/barrikade-lens /var/log/barrikade-lens
```

## Credential and imaging rules

- Never clone `/Library/Application Support/Barrikade/Lens`, `%ProgramData%\Barrikade\Lens`, or `/etc/barrikade-lens` into a golden image. Enroll after the machine receives its durable device identity.
- Treat bootstrap codes as one-time, short-lived credentials. Remove them from MDM variables after the rollout window, and revoke unused profiles in Hub.
- Collector access tokens last 15 minutes. Refresh credentials rotate at use, and source revocation invalidates refresh credentials immediately. Hub checks source state on every snapshot, including requests with a previously issued access token.
- Normal uninstall deliberately preserves identity and configuration. Use the documented full-cleanup path only for decommissioning; it is destructive and requires a new endpoint enrollment afterward.

The npm launcher and direct signed binaries remain available for developer evaluation and one-off pilots. Fleet deployments should use the native packages so the installed runtime has no Node.js dependency.
