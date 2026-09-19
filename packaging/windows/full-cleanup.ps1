[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param()

$ErrorActionPreference = 'Stop'
$product = Get-ItemProperty 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction SilentlyContinue |
    Where-Object { $_.DisplayName -eq 'Barrikade Lens' } |
    Select-Object -First 1

if ($product -and $PSCmdlet.ShouldProcess('Barrikade Lens', 'Uninstall MSI and permanently delete local identity and configuration')) {
    $uninstaller = Start-Process msiexec.exe -Wait -PassThru -ArgumentList "/x $($product.PSChildName) /qn /norestart"
    if ($uninstaller.ExitCode -notin @(0, 1605, 3010)) { throw "Barrikade Lens uninstall failed with exit code $($uninstaller.ExitCode)" }
}

$stateDirectory = Join-Path $env:ProgramData 'Barrikade\Lens'
if ($PSCmdlet.ShouldProcess($stateDirectory, 'Permanently delete local identity, credentials, configuration, and collector logs')) {
    Remove-Item -LiteralPath $stateDirectory -Recurse -Force -ErrorAction SilentlyContinue
}
