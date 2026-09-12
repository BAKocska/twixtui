param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('amd64', 'arm64')]
    [string] $ExpectedArch,
    [ValidateSet('all', 'artifact')]
    [string] $Scope = 'all',
    [string] $Binary = '',
    [string] $ReportDirectory = (Join-Path $env:RUNNER_TEMP 'windows-tests')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'This payload must execute on native Windows, not WSL or a cross-compiler.' }
if ($Scope -eq 'artifact' -and [string]::IsNullOrWhiteSpace($Binary)) {
    throw 'Artifact verification requires an explicit downloaded binary; rebuilding is forbidden.'
}
$target = @(& go env GOOS GOARCH)
if ($LASTEXITCODE -ne 0 -or $target.Count -ne 2 -or $target[0] -ne 'windows' -or $target[1] -ne $ExpectedArch) {
    throw "Wrong test toolchain target: $($target -join '/') (expected windows/$ExpectedArch)"
}
$env:TWIXTUI_EXPECT_GOOS = 'windows'
$env:TWIXTUI_EXPECT_GOARCH = $ExpectedArch
$env:NO_COLOR = '1'
if ($Binary) {
    if (-not (Test-Path -LiteralPath $Binary -PathType Leaf)) { throw "Artifact is missing: $Binary" }
    $env:TWIXTUI_E2E_BINARY = (Resolve-Path -LiteralPath $Binary).Path
} else {
    Remove-Item Env:TWIXTUI_E2E_BINARY -ErrorAction SilentlyContinue
}
New-Item -ItemType Directory -Force -Path $ReportDirectory | Out-Null
Write-Host "Native host: $([System.Runtime.InteropServices.RuntimeInformation]::OSDescription)"
Write-Host "Architecture: $([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture); Go $($target -join '/')"
Write-Host "PowerShell: $($PSVersionTable.PSVersion)"
$package = if ($Scope -eq 'artifact') { './internal/e2e' } else { './...' }
$eventsPath = Join-Path $ReportDirectory 'test.jsonl'
& go test -json -count=1 -parallel 4 -timeout 10m $package | Tee-Object -FilePath $eventsPath
$testExit = $LASTEXITCODE

$e2e = 'github.com/BAKocska/twixtui/internal/e2e'
$passes = @{}
$allPasses = @{}
$skips = [System.Collections.Generic.List[string]]::new()
$totalPasses = 0
foreach ($line in Get-Content -LiteralPath $eventsPath) {
    if ([string]::IsNullOrWhiteSpace($line)) { continue }
    $event = ConvertFrom-Json -InputObject $line -AsHashtable
    if ($event['Test'] -and $event['Action'] -eq 'pass') {
        $totalPasses++
        $allPasses[$event['Package'] + '/' + $event['Test']] = $true
    }
    if ($event['Package'] -ne $e2e -or -not $event['Test']) { continue }
    if ($event['Action'] -eq 'pass') { $passes[$event['Test']] = $true }
    if ($event['Action'] -eq 'skip') { $skips.Add($event['Test']) }
}
$required = @(
    'TestNativeExecutionIdentity',
    'TestCaptureSeesProgramOutput',
    'TestWaitForCanFail',
    'TestDetectsImmediateExit',
    'TestResizeReachesTheProgram',
    'TestAlternateScreenIsCaptured',
    'TestSendTextAndKeys',
    'TestPowerShellCompletesTiersAndProfiles',
    'TestStyledGlyphsAreCapturedBothWays',
    'TestRecordsSurvivePathsWithSpacesAndUnicode',
    'TestRecordRefusalsLeaveNothingBehind',
    'TestDirectNetworkGamePlaysAndResumes',
    'TestRelayedNetworkGamePlays',
    'TestTwoTerminalsPlayByCode',
    'TestHintScopesNoRouteClaimsOnWideAndNarrowBoards',
    'TestReplayEntryJumpPreservesRecordAcrossResize',
    'TestLeaderboardHistoryReplayFromTheMenu'
)
$missing = @($required | Where-Object { -not $passes.ContainsKey($_) })
$missingStorage = @()
if ($Scope -eq 'all') {
    $requiredStorage = @(
        'winfs/TestALockIsHeldAgainstAnotherProcess',
        'winfs/TestASecondExclusiveLockWaitsForTheFirstInsideOneProcess',
        'winfs/TestSharedLocksOverlapAndAnExclusiveOneWaitsForThem',
        'winfs/TestASharedLockGivesUpWhereNothingMayBeWritten',
        'winfs/TestAnExclusiveLockFailsWhereNothingMayBeWritten',
        'winfs/TestAReplacementLandsWhileTheFileIsBeingRead',
        'winfs/TestAReplacementIsRefusedWhileDeletionIsForbidden',
        'winfs/TestLongPathsPreserveReplacementAndLocking',
        'winfs/TestHeldLockCannotBeDeleted',
        'profile/TestAnotherProcessWaitsForThisOnesWrite',
        'profile/TestConcurrentCreateAcrossStores',
        'profile/TestReadingAStoreNothingMayWriteTo',
        'profile/TestAWriteThatCannotReplaceTheFileKeepsThePreviousProfiles',
        'leaderboard/TestConcurrentRecordAcrossBoards',
        'leaderboard/TestReadingABoardNothingMayWriteTo',
        'leaderboard/TestARecordThatCannotReplaceTheFileKeepsThePreviousResults',
        'leaderboard/TestFirstWriteToAV1BoardKeepsItsRows',
        'leaderboard/TestRetryingTheSameFinalResultWritesNothing',
        'leaderboard/TestADifferentResultForARecordedGameIsRefused',
        'leaderboard/TestAnotherProcessRecordingTheSameGame',
        'app/TestTwoWindowsFinishingAGameTheSameWayCreditItOnce',
        'app/TestARematchIsRatedAsItsOwnGame',
        'app/TestLeaderboardChecksTheGameAgainWhenItIsOpened',
        'app/TestLeaderboardOpensOnlyTheGameTheResultNames',
        'gamestore/TestMissingGamesAreDistinguishedFromUnreadableGames',
        'gamestore/TestOnlyOneFinishOfAGameIsStored',
        'gamestore/TestReadingAStoreNothingMayWriteTo',
        'gamestore/TestASaveThatCannotReplaceTheGameKeepsTheStoredOne',
        'gamestore/TestWindowsDeviceGameIDsAreRejected'
    )
    $missingStorage = @($requiredStorage | Where-Object {
        -not $allPasses.ContainsKey('github.com/BAKocska/twixtui/internal/' + $_)
    })
}
$summary = [ordered]@{
    os = [System.Runtime.InteropServices.RuntimeInformation]::OSDescription
    architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    goos = $target[0]
    goarch = $target[1]
    binary = $Binary
    test_exit = $testExit
    passed_tests_and_subtests = $totalPasses
    e2e_passes = $passes.Count
    e2e_skips = $skips.ToArray()
    missing_required_tests = $missing
    missing_required_storage_tests = $missingStorage
}
$summary | ConvertTo-Json -Depth 5 | Set-Content -Encoding utf8 -LiteralPath (Join-Path $ReportDirectory 'summary.json')
if ($testExit -ne 0) { throw "Native Windows tests failed (exit $testExit); see $eventsPath" }
if ($skips.Count -ne 0) { throw "Windows e2e skipped instead of exercising ConPTY: $($skips -join ', ')" }
if ($missing.Count -ne 0) { throw "Windows e2e did not pass required controls/scenarios: $($missing -join ', ')" }
if ($missingStorage.Count -ne 0) { throw "Windows storage proof missing or skipped: $($missingStorage -join ', ')" }
Write-Host "$($passes.Count) native Windows e2e tests/subtests passed, no skips; all required controls exercised."
