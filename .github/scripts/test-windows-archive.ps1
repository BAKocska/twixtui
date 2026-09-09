param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('amd64', 'arm64')]
    [string] $ExpectedArch,
    [Parameter(Mandatory = $true)]
    [string] $ExpectedVersion,
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-fA-F0-9]{40}$')]
    [string] $ExpectedCommit,
    [Parameter(Mandatory = $true)]
    [string] $Archive,
    [Parameter(Mandatory = $true)]
    [string] $Checksums,
    [string] $ReportDirectory = (Join-Path $env:RUNNER_TEMP 'windows-archive-tests')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Windows archives must execute on native Windows.' }
$name = Split-Path -Leaf $Archive
if ($name -cne "twixtui_${ExpectedVersion}_windows_${ExpectedArch}.zip") {
    throw "Unexpected Windows archive: $name"
}
$lines = @(Get-Content -LiteralPath $Checksums | Where-Object { ($_ -split '\s+')[-1] -ceq $name })
if ($lines.Count -ne 1) { throw 'Missing or ambiguous archive checksum.' }
$expected = ($lines[0] -split '\s+')[0]
if ($expected -notmatch '^[a-fA-F0-9]{64}$') { throw 'Malformed archive checksum.' }
$actual = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash
if ($expected -ne $actual) { throw 'Windows archive checksum mismatch.' }

$destination = Join-Path $ReportDirectory 'unpacked Windows ZIP'
if (Test-Path -LiteralPath $destination) { throw "Archive destination is not fresh: $destination" }
New-Item -ItemType Directory -Force -Path $ReportDirectory | Out-Null
Expand-Archive -LiteralPath $Archive -DestinationPath $destination
$binary = Join-Path $destination 'twixtui.exe'
if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'The archive contains no twixtui.exe.' }
$reported = (& $binary version | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or -not $reported.StartsWith("twixtui $ExpectedVersion ", [StringComparison]::Ordinal) -or
    $reported -notmatch [regex]::Escape($ExpectedCommit)) {
    throw "Archive binary has the wrong version/commit: $reported"
}
@{ commit = $ExpectedCommit; archive = $name; sha256 = $actual; version = $reported } |
    ConvertTo-Json | Set-Content -Encoding utf8 -LiteralPath (Join-Path $ReportDirectory 'artifact.json')

# This requires the exact extracted executable. The test driver refuses a
# missing artifact and must never silently build a source substitute.
& (Join-Path $PSScriptRoot 'test-windows.ps1') -ExpectedArch $ExpectedArch -Scope artifact -Binary $binary -ReportDirectory $ReportDirectory
