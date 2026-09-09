//go:build windows

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Exercise PowerShell's completion engine, not just generated-script text.
// The config path contains both spaces and Unicode so quoting mistakes cannot
// silently complete against an unrelated default profile store.
func TestPowerShellCompletesTiersAndProfiles(t *testing.T) {
	t.Parallel()
	bin := binary(t)
	cfg := filepath.Join(t.TempDir(), "player data Árvíz")
	corrRun(t, bin, cfg, "profile", "create", "Ada")
	corrRun(t, bin, cfg, "profile", "create", "Linus")
	fallback := filepath.Join(t.TempDir(), "wrong fallback store")
	corrRun(t, bin, fallback, "profile", "create", "Decoy")
	script := `param([string]$Exe, [string]$Config, [string]$Fallback)
$ErrorActionPreference = 'Stop'
$env:PATH = (Split-Path -Parent $Exe) + [IO.Path]::PathSeparator + $env:PATH
$env:TWIXTUI_CONFIG_DIR = $Fallback
$generated = & $Exe completion powershell
if ($LASTEXITCODE -ne 0) { throw 'completion generation failed' }
Invoke-Expression ($generated -join [Environment]::NewLine)
$escaped = $Config.Replace("'", "''")
$prefix = "twixtui --config '$escaped' "
$tierLine = $prefix + 'play bot --tier '
$profileLine = $prefix + 'profile use '
$tier = [System.Management.Automation.CommandCompletion]::CompleteInput($tierLine, $tierLine.Length, $null)
$profile = [System.Management.Automation.CommandCompletion]::CompleteInput($profileLine, $profileLine.Length, $null)
@{
  tiers = @($tier.CompletionMatches | ForEach-Object { $_.CompletionText })
  profiles = @($profile.CompletionMatches | ForEach-Object { $_.CompletionText })
} | ConvertTo-Json -Compress
`
	path := filepath.Join(t.TempDir(), "completion probe.ps1")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pwsh", "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path, bin, cfg, fallback)
	cmd.Env = append(os.Environ(), "TWIXTUI_CONFIG_DIR="+cfg, "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell completion: %v\n%s", err, out)
	}
	var got struct {
		Tiers    []string `json:"tiers"`
		Profiles []string `json:"profiles"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("PowerShell completion output: %v\n%s", err, out)
	}
	for _, tier := range []string{"beginner", "intermediate", "pro", "max"} {
		if !slices.Contains(got.Tiers, tier) {
			t.Errorf("native completion omitted %s: %v", tier, got.Tiers)
		}
	}
	for _, name := range []string{"Ada", "Linus"} {
		if !slices.Contains(got.Profiles, name) {
			t.Errorf("native completion used the wrong profile store or omitted %s: %v", name, got.Profiles)
		}
	}
	if slices.Contains(got.Profiles, "Decoy") {
		t.Fatalf("completion fell back to the environment instead of the quoted config argument: %v", got.Profiles)
	}
}
