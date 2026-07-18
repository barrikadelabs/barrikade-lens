$ErrorActionPreference = "Stop"

if (-not $env:BARRIKADE_LENS_ENROLLMENT_CODE) { throw "Supply BARRIKADE_LENS_ENROLLMENT_CODE as a protected Intune value" }
if (-not $env:BARRIKADE_LENS_HUB) { throw "Supply BARRIKADE_LENS_HUB" }

$binary = Join-Path $env:ProgramFiles "Barrikade Lens\barrikade-lens.exe"
$configuration = Join-Path $env:ProgramData "Barrikade\Lens\config.json"
& $binary enroll $env:BARRIKADE_LENS_ENROLLMENT_CODE --hub $env:BARRIKADE_LENS_HUB --config $configuration
if ($LASTEXITCODE -ne 0) { throw "Barrikade Lens enrollment failed" }
Set-Service -Name BarrikadeLens -StartupType Automatic
Start-Service -Name BarrikadeLens
