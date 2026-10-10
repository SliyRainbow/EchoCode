[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version,
    [string]$WailsExecutable = 'wails',
    [string]$InnoCompiler = (Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6\ISCC.exe')
)

$ErrorActionPreference = 'Stop'
$releaseRepo = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$releaseDir = Join-Path $releaseRepo "build\releases\$Version"
$utf8 = [System.Text.UTF8Encoding]::new($false)
if (!(Test-Path -LiteralPath $InnoCompiler -PathType Leaf)) {
    throw 'A preinstalled Inno Setup compiler is required; this script does not install build tools.'
}
$wailsConfig = Get-Content -LiteralPath (Join-Path $releaseRepo 'wails.json') -Encoding UTF8 -Raw | ConvertFrom-Json
$package = Get-Content -LiteralPath (Join-Path $releaseRepo 'frontend\package.json') -Encoding UTF8 -Raw | ConvertFrom-Json
$resources = Get-Content -LiteralPath (Join-Path $releaseRepo 'build\windows\info.json') -Encoding UTF8 -Raw | ConvertFrom-Json
$versionSource = [IO.File]::ReadAllText((Join-Path $releaseRepo 'version.go'), $utf8)
$installerSource = [IO.File]::ReadAllText((Join-Path $releaseRepo 'build\windows\echocode-installer.iss'), $utf8)
$escapedVersion = [regex]::Escape($Version)
if ($wailsConfig.info.productVersion -ne $Version -or $package.version -ne $Version -or
    $resources.fixed.file_version -ne "$Version.0" -or $resources.fixed.product_version -ne "$Version.0" -or
    $versionSource -notmatch ('appVersion\s*=\s*"' + $escapedVersion + '"') -or
    $installerSource -notmatch ('#define MyAppVersion "' + $escapedVersion + '"')) {
    throw 'Application, frontend, Windows resource and installer versions must match the requested release.'
}
$notesPath = Join-Path $releaseRepo "docs\releases\v$Version.md"
if (!(Test-Path -LiteralPath $notesPath -PathType Leaf)) { throw 'Versioned UTF-8 release notes are required.' }
if ((Test-Path -LiteralPath $releaseDir) -and @(Get-ChildItem -LiteralPath $releaseDir -Force).Count -gt 0) {
    throw "Release output must be empty: $releaseDir"
}
New-Item -ItemType Directory -Force -Path $releaseDir | Out-Null
Push-Location $releaseRepo
try {
    $releaseCommit = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $releaseCommit -notmatch '^[0-9a-f]{40}$') { throw 'Cannot resolve release commit.' }
    $releaseDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    & $WailsExecutable build -clean -platform windows/amd64 -ldflags "-X main.appVersion=$Version -X main.appCommit=$releaseCommit -X main.appBuildDate=$releaseDate"
    if ($LASTEXITCODE -ne 0) { throw 'Wails production build failed.' }
    $binary = Join-Path $releaseRepo 'build\bin\EchoCode.exe'
    $fileVersion = (Get-Item -LiteralPath $binary).VersionInfo
    if ($fileVersion.FileVersion -ne "$Version.0" -or $fileVersion.ProductVersion -ne $Version) {
        throw 'Executable Windows version resources do not match the release.'
    }
    $binaryText = [Text.Encoding]::ASCII.GetString([IO.File]::ReadAllBytes($binary))
    if ($binaryText.Contains('ECHOCODE_ACCEPTANCE_CDP_PORT') -or $binaryText.Contains('ECHOCODE_ACCEPTANCE_ROOT')) {
        throw 'Acceptance-only instrumentation found in production executable.'
    }
    Copy-Item -LiteralPath $binary -Destination (Join-Path $releaseDir 'EchoCode.exe')
    & $InnoCompiler '/Qp' "/O$releaseDir" (Join-Path $releaseRepo 'build\windows\echocode-installer.iss')
    if ($LASTEXITCODE -ne 0) { throw 'Inno Setup compilation failed.' }
    $setupName = "EchoCode-setup-$Version.exe"
    $setup = Join-Path $releaseDir $setupName
    if (!(Test-Path -LiteralPath $setup -PathType Leaf)) { throw 'Expected installer was not produced.' }
    $setupVersion = [Diagnostics.FileVersionInfo]::GetVersionInfo($setup)
    $setupFixedVersion = "$($setupVersion.FileMajorPart).$($setupVersion.FileMinorPart).$($setupVersion.FileBuildPart).$($setupVersion.FilePrivatePart)"
    if ($setupFixedVersion -ne "$Version.0" -or $setupVersion.ProductVersion -ne $Version) {
        throw "Installer version mismatch: fixed=$setupFixedVersion; text=$($setupVersion.FileVersion); product=$($setupVersion.ProductVersion)."
    }
    $zipName = "EchoCode-$Version-windows-amd64-portable.zip"
    $zipPath = Join-Path $releaseDir $zipName
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::Open($zipPath, [IO.Compression.ZipArchiveMode]::Create)
    try {
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $binary, 'EchoCode.exe') | Out-Null
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $notesPath, 'RELEASE-NOTES.md') | Out-Null
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, (Join-Path $releaseRepo 'LICENSE'), 'LICENSE') | Out-Null
    } finally { $zip.Dispose() }
    $files = @()
    $checksums = @()
    foreach ($name in @('EchoCode.exe', $setupName, $zipName)) {
        $path = Join-Path $releaseDir $name
        $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        $line = "$hash  $name"
        $checksums += $line
        [IO.File]::WriteAllText("$path.sha256", $line + "`n", $utf8)
        $files += [ordered]@{ name = $name; sha256 = $hash; size = (Get-Item -LiteralPath $path).Length }
    }
    [IO.File]::WriteAllText((Join-Path $releaseDir 'SHA256SUMS.txt'), ($checksums -join "`n") + "`n", $utf8)
    $manifest = [ordered]@{ version = $Version; commit = $releaseCommit; builtAt = $releaseDate; files = $files }
    [IO.File]::WriteAllText((Join-Path $releaseDir 'release-build.json'), ($manifest | ConvertTo-Json -Depth 5), $utf8)
    Write-Output "Release assets ready: $releaseDir"
} finally { Pop-Location }
