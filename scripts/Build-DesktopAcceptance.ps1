param(
    [string]$GoExecutable = 'go',
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build\native-acceptance')
)
$ErrorActionPreference = 'Stop'
$echoRepository = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$echoOutput = [IO.Path]::GetFullPath($OutputDirectory)
[IO.Directory]::CreateDirectory($echoOutput) | Out-Null
$echoSourcePath = Join-Path $echoRepository 'third_party\go-webview2\pkg\edge\chromium.go'
$echoOverlaySource = Join-Path $echoOutput 'chromium-acceptance.go'
$echoOverlayJSON = Join-Path $echoOutput 'overlay.json'
$echoSource = [IO.File]::ReadAllText($echoSourcePath)
$echoMarker = 'browserArgs := strings.Join(e.AdditionalBrowserArgs, " ")'
$echoReplacement = @'
browserArgs := strings.Join(e.AdditionalBrowserArgs, " ")
    if port := os.Getenv("ECHOCODE_ACCEPTANCE_CDP_PORT"); port != "" && !strings.Contains(browserArgs, "--remote-debugging-port=") {
        browserArgs += " --remote-debugging-address=127.0.0.1 --remote-debugging-port=" + port
        log.Printf("Acceptance browser arguments: %s", browserArgs)
    }
'@
if (-not $echoSource.Contains($echoMarker)) { throw 'WebView2 source no longer matches the acceptance overlay' }
$echoUTF8 = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText($echoOverlaySource, $echoSource.Replace($echoMarker, $echoReplacement), $echoUTF8)
$echoReplacements = @{}
$echoReplacements[$echoSourcePath] = $echoOverlaySource
$echoAppSourcePath = Join-Path $echoRepository 'app.go'
$echoAppOverlay = Join-Path $echoOutput 'app-acceptance.go'
$echoAppSource = [IO.File]::ReadAllText($echoAppSourcePath)
foreach ($echoFunction in @('temporaryWorkspaceRoot','browserDownloadsDir')) {
    $echoFunctionMarker = 'func ' + $echoFunction + '() string {'
    $echoFunctionReplacement = $echoFunctionMarker + "`n" + ' if root := os.Getenv("ECHOCODE_ACCEPTANCE_ROOT"); root != "" { return filepath.Join(root, "' + $echoFunction + '") }'
    if (-not $echoAppSource.Contains($echoFunctionMarker)) { throw 'Application paths no longer match the acceptance overlay' }
    $echoAppSource = $echoAppSource.Replace($echoFunctionMarker, $echoFunctionReplacement)
}
[IO.File]::WriteAllText($echoAppOverlay, $echoAppSource, $echoUTF8)
$echoReplacements[$echoAppSourcePath] = $echoAppOverlay
[IO.File]::WriteAllText($echoOverlayJSON, (@{ Replace = $echoReplacements } | ConvertTo-Json -Depth 4), $echoUTF8)
Push-Location -LiteralPath $echoRepository
try {
    # Instrument only the test binary. The source tree and release binary keep
    # the production WebView2 policy that rejects remote-debugging overrides.
    & $GoExecutable build "-overlay=$echoOverlayJSON" -tags 'production,devtools' -ldflags '-H windowsgui' -o (Join-Path $echoOutput 'EchoCode-acceptance.exe') .
    if ($LASTEXITCODE -ne 0) { throw "Acceptance build failed: $LASTEXITCODE" }
}
finally { Pop-Location }
Write-Output (Join-Path $echoOutput 'EchoCode-acceptance.exe')
