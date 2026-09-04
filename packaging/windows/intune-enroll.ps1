$ErrorActionPreference = "Stop"

if (-not $env:BARRIKADE_LENS_ENROLLMENT_CODE) { throw "Supply BARRIKADE_LENS_ENROLLMENT_CODE as a protected Intune value" }
if (-not $env:BARRIKADE_LENS_HUB) { throw "Supply BARRIKADE_LENS_HUB" }

$binary = Join-Path $env:ProgramFiles "Barrikade Lens\barrikade-lens.exe"
$configuration = Join-Path $env:ProgramData "Barrikade\Lens\config.json"
$configurationDirectory = Split-Path $configuration
New-Item -ItemType Directory -Force -Path $configurationDirectory | Out-Null
$acl = Get-Acl $configurationDirectory
$acl.SetAccessRuleProtection($true, $false)
$acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new("SYSTEM", "FullControl", "ContainerInherit,ObjectInherit", "None", "Allow"))
$acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new("BUILTIN\Administrators", "FullControl", "ContainerInherit,ObjectInherit", "None", "Allow"))
Set-Acl -Path $configurationDirectory -AclObject $acl
& $binary enroll $env:BARRIKADE_LENS_ENROLLMENT_CODE --hub $env:BARRIKADE_LENS_HUB --config $configuration --install
if ($LASTEXITCODE -ne 0) { throw "Barrikade Lens enrollment failed" }
