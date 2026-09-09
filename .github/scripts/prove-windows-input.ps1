param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('amd64', 'arm64')]
    [string] $ExpectedArch,
    [string] $ReportDirectory = (Join-Path $env:RUNNER_TEMP 'windows-input-proof')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$PSNativeCommandUseErrorActionPreference = $false
if (-not $IsWindows) { throw 'Input proof requires native Windows.' }
$target = @(& go env GOOS GOARCH)
if ($LASTEXITCODE -ne 0 -or $target.Count -ne 2 -or $target[0] -ne 'windows' -or $target[1] -ne $ExpectedArch) {
    throw 'Input proof is using the wrong native toolchain.'
}
$module = & go list -m -json github.com/charmbracelet/ultraviolet | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $module.Replace.Path -ne 'github.com/BAKocska/ultraviolet' -or
    $module.Replace.Version -ne 'v0.0.0-20260909131952-194026188632') {
    throw 'Input proof must exercise the exact pinned fix.'
}
New-Item -ItemType Directory -Force -Path $ReportDirectory | Out-Null
$scratch = Join-Path $env:RUNNER_TEMP ('uv-input-proof-' + [Guid]::NewGuid().ToString('N'))
Copy-Item -LiteralPath $module.Dir -Destination $scratch -Recurse
$source = Join-Path $scratch 'terminal_reader_windows.go'
(Get-Item -LiteralPath $source).IsReadOnly = $false
$fixed = Get-Content -Raw -LiteralPath $source
$hadWork = Test-Path Env:GOWORK
$oldWork = $env:GOWORK
$env:GOWORK = 'off'

function Invoke-InputProbe([string] $Name, [string] $ExpectedFailure = '') {
    $events = Join-Path $ReportDirectory "$Name.jsonl"
    & go -C $scratch test -json -count=1 -run '^TestSerializeWin32Input' -timeout 30s . | Tee-Object -FilePath $events
    $exit = $LASTEXITCODE
    $rows = @(Get-Content -LiteralPath $events | ForEach-Object { ConvertFrom-Json -AsHashtable $_ })
    $passes = @($rows | Where-Object { $_['Action'] -eq 'pass' -and $_['Test'] } | ForEach-Object { $_['Test'] })
    $failures = @($rows | Where-Object { $_['Action'] -eq 'fail' -and $_['Test'] } | ForEach-Object { $_['Test'] })
    if ($ExpectedFailure) {
        if ($exit -eq 0 -or $failures -notcontains $ExpectedFailure) {
            throw "$Name did not fail the intended regression ($ExpectedFailure); build errors and timeouts do not count."
        }
    } else {
        $required = @(
            'TestSerializeWin32InputVTPasteWithShift',
            'TestSerializeWin32InputVTModifiersProduceNoInput',
            'TestSerializeWin32InputVTModifierWithCharacterKeepsText',
            'TestSerializeWin32InputVTKeepsGenuineNul',
            'TestSerializeWin32InputVTSurrogatePairSurvivesModifier',
            'TestSerializeWin32InputNonVTEncodesModifiers'
        )
        if ($exit -ne 0 -or @($required | Where-Object { $passes -notcontains $_ }).Count -ne 0) {
            throw 'The fixed dependency did not pass every required input regression.'
        }
    }
    @{ probe = $Name; exit = $exit; passes = $passes; failures = $failures } |
        ConvertTo-Json -Depth 4 | Set-Content -Encoding utf8 -LiteralPath (Join-Path $ReportDirectory "$Name.json")
}

try {
    Invoke-InputProbe 'fixed'
    $baseline = & go mod download -json "github.com/charmbracelet/ultraviolet@$($module.Version)" | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw 'Could not obtain the unchanged upstream baseline.' }
    $before = Get-Content -Raw -LiteralPath (Join-Path $baseline.Dir 'terminal_reader_windows.go')
    Set-Content -NoNewline -Encoding utf8 -LiteralPath $source -Value $before
    Invoke-InputProbe 'upstream-baseline' 'TestSerializeWin32InputVTPasteWithShift'

    $guard = 'kevent.Char == 0 && isModifierVirtualKeyCode(kevent.VirtualKeyCode)'
    if ([regex]::Matches($fixed, [regex]::Escape($guard)).Count -ne 1) { throw 'Mutation guard no longer identifies exactly one predicate.' }
    Set-Content -NoNewline -Encoding utf8 -LiteralPath $source -Value $fixed.Replace($guard, 'kevent.Char == 0')
    Invoke-InputProbe 'global-nul-drop' 'TestSerializeWin32InputVTKeepsGenuineNul'
    @{ architecture = $ExpectedArch; fixed = $module.Replace.Version; baseline = $module.Version; mutations_rejected = 2 } |
        ConvertTo-Json | Set-Content -Encoding utf8 -LiteralPath (Join-Path $ReportDirectory 'summary.json')
} finally {
    if ($hadWork) { $env:GOWORK = $oldWork } else { Remove-Item Env:GOWORK -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $scratch -Recurse -Force
}
