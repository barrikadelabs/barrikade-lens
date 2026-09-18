[CmdletBinding()]
param(
    [string]$MsiPath,
    [string]$HubUrl = $env:BARRIKADE_LENS_HUB
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

if (-not $env:BARRIKADE_LENS_ENROLLMENT_CODE) { throw "Supply BARRIKADE_LENS_ENROLLMENT_CODE as a protected Intune value" }
if (-not $HubUrl) { throw "Supply BARRIKADE_LENS_HUB or -HubUrl" }
if ($HubUrl.Contains('"') -or $HubUrl.Contains("`r") -or $HubUrl.Contains("`n")) { throw "The Lens Hub URL is invalid" }

$bootstrapCode = $env:BARRIKADE_LENS_ENROLLMENT_CODE
Remove-Item Env:BARRIKADE_LENS_ENROLLMENT_CODE -ErrorAction SilentlyContinue

try {
    if ($MsiPath) {
        $installer = Start-Process msiexec.exe -Wait -PassThru -WindowStyle Hidden -ArgumentList @('/i', ('"{0}"' -f $MsiPath), '/qn', '/norestart')
        if ($installer.ExitCode -notin @(0, 3010)) { throw "Barrikade Lens MSI installation failed with exit code $($installer.ExitCode)" }
    }

    $binary = Join-Path $env:ProgramFiles "Barrikade Lens\barrikade-lens.exe"
    if (-not (Test-Path -LiteralPath $binary)) { throw "Barrikade Lens is not installed at $binary" }

    $configuration = Join-Path $env:ProgramData "Barrikade\Lens\config.json"
    $configurationDirectory = Split-Path $configuration
    New-Item -ItemType Directory -Force -Path $configurationDirectory | Out-Null
    $acl = New-Object System.Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    $inheritance = [System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
    $propagation = [System.Security.AccessControl.PropagationFlags]::None
    $allow = [System.Security.AccessControl.AccessControlType]::Allow
    $acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new('SYSTEM', 'FullControl', $inheritance, $propagation, $allow))
    $acl.AddAccessRule([System.Security.AccessControl.FileSystemAccessRule]::new('BUILTIN\Administrators', 'FullControl', $inheritance, $propagation, $allow))
    Set-Acl -LiteralPath $configurationDirectory -AclObject $acl

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $binary
    $startInfo.Arguments = 'enroll --enrollment-code-stdin --hub "{0}" --config "{1}" --install' -f $HubUrl, $configuration
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    if (-not $process.Start()) { throw "Barrikade Lens enrollment could not be started" }
    $process.StandardInput.WriteLine($bootstrapCode)
    $process.StandardInput.Close()
    $bootstrapCode = $null
    $standardOutput = $process.StandardOutput.ReadToEnd()
    $null = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if ($process.ExitCode -ne 0) { throw "Barrikade Lens enrollment failed with exit code $($process.ExitCode); retry with a fresh bootstrap credential" }
    if ($standardOutput) { Write-Output $standardOutput.TrimEnd() }
}
finally {
    $bootstrapCode = $null
    Remove-Item Env:BARRIKADE_LENS_ENROLLMENT_CODE -ErrorAction SilentlyContinue
    if ($process) { $process.Dispose() }
}
