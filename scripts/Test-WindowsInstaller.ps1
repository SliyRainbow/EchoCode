[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$ReleaseDirectory)

$ErrorActionPreference = 'Stop'
# Run only on an ephemeral GitHub runner, never over a user's existing install.
if ($env:GITHUB_ACTIONS -ne 'true' -or [string]::IsNullOrWhiteSpace($env:RUNNER_TEMP)) {
    throw 'Installer smoke tests require an isolated GitHub Actions runner.'
}
$manifest = Get-Content -LiteralPath (Join-Path $ReleaseDirectory 'release-build.json') -Encoding UTF8 -Raw | ConvertFrom-Json
$setup = Join-Path $ReleaseDirectory "EchoCode-setup-$($manifest.version).exe"
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{3E9F8C7A-0D4E-4B6A-9C1F-5A9B4C7D2E01}_is1'
if (Test-Path -LiteralPath $uninstallKey) { throw 'An existing EchoCode installation must not be touched.' }
$testRoot = [IO.Path]::GetFullPath((Join-Path $env:RUNNER_TEMP ('EchoCode-install-test-' + [guid]::NewGuid().ToString('N'))))
$runnerRoot = [IO.Path]::GetFullPath($env:RUNNER_TEMP).TrimEnd('\') + '\'
if (!$testRoot.StartsWith($runnerRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'Test install path escaped runner temporary directory.' }
$setupArgs = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-', '/NOICONS', '/TASKS=""', ('/DIR="' + $testRoot + '"'))
$installed = $false
try {
    $process = Start-Process -FilePath $setup -ArgumentList $setupArgs -WindowStyle Hidden -PassThru -Wait
    if ($process.ExitCode -ne 0) { throw "Installer exited with $($process.ExitCode)." }
    $installed = $true
    $expected = ($manifest.files | Where-Object { $_.name -eq 'EchoCode.exe' }).sha256
    $actual = (Get-FileHash -LiteralPath (Join-Path $testRoot 'EchoCode.exe') -Algorithm SHA256).Hash
    if ($actual -ne $expected) { throw 'Installed executable differs from standalone release executable.' }
    $registration = Get-ItemProperty -LiteralPath $uninstallKey
    if ($registration.DisplayVersion -ne $manifest.version) { throw 'Installed version registration mismatch.' }
    Write-Output "PASS installer payload and version $($manifest.version)"
} finally {
    $uninstaller = Join-Path $testRoot 'unins000.exe'
    if ($installed -and (Test-Path -LiteralPath $uninstaller -PathType Leaf)) {
        $process = Start-Process -FilePath $uninstaller -ArgumentList @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART') -WindowStyle Hidden -PassThru -Wait
        if ($process.ExitCode -ne 0) { throw "Uninstaller exited with $($process.ExitCode)." }
        if ((Test-Path -LiteralPath (Join-Path $testRoot 'EchoCode.exe')) -or (Test-Path -LiteralPath $uninstallKey)) { throw 'Uninstall left application or registration behind.' }
        Write-Output 'PASS uninstall removed application and registration'
    }
}
