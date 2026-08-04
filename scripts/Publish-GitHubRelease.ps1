[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v\d+\.\d+\.\d+$')]
    [string]$Tag,

    [string]$Repository = 'SliyRainbow/EchoCode'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$notesPath = Join-Path $repoRoot (Join-Path 'docs\releases' ($Tag + '.md'))

if (-not (Test-Path -LiteralPath $notesPath -PathType Leaf)) {
    throw "UTF-8 release notes not found: $notesPath"
}

$utf8 = New-Object System.Text.UTF8Encoding($false)
$body = [System.IO.File]::ReadAllText($notesPath, $utf8)
if ([string]::IsNullOrWhiteSpace($body)) {
    throw "Release notes are empty: $notesPath"
}
if ($body.Contains([char]0xFFFD)) {
    throw "Release notes contain the Unicode replacement character; fix the source file before publishing."
}

$token = $env:GITHUB_TOKEN
if ([string]::IsNullOrWhiteSpace($token)) {
    $token = $env:GH_TOKEN
}
if ([string]::IsNullOrWhiteSpace($token)) {
    throw 'Set GITHUB_TOKEN or GH_TOKEN before updating a GitHub Release.'
}

$headers = @{
    Authorization = "Bearer $token"
    Accept = 'application/vnd.github+json'
    'X-GitHub-Api-Version' = '2022-11-28'
    'User-Agent' = 'EchoCode-release-publisher'
}
$apiBase = "https://api.github.com/repos/$Repository"
$release = Invoke-RestMethod -Headers $headers -Uri "$apiBase/releases/tags/$Tag"
$payload = @{
    name = if ([string]::IsNullOrWhiteSpace($release.name)) { "EchoCode $Tag" } else { $release.name }
    body = $body
} | ConvertTo-Json -Depth 4

$updated = Invoke-RestMethod `
    -Method Patch `
    -Headers $headers `
    -ContentType 'application/json; charset=utf-8' `
    -Body ([System.Text.Encoding]::UTF8.GetBytes($payload)) `
    -Uri "$apiBase/releases/$($release.id)"

[pscustomobject]@{
    Tag = $Tag
    URL = $updated.html_url
    NotesFile = $notesPath
    NotesBytes = $utf8.GetByteCount($body)
}
