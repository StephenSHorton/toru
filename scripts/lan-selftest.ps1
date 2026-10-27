<#
.SYNOPSIS
  One-time elevated end-to-end check of Toru LAN on this PC.

.DESCRIPTION
  Builds toru-lan.exe and the selftest, then (elevated, one UAC prompt):
  installs Toru LAN, starts the service, drives it over the pipe like the
  GUI, checks the adapter (address, metric 1, routes, Private), the firewall
  rules and the service security descriptor, runs the broadcast go/no-go
  (a broadcast written into the adapter must reach a socket bound to
  0.0.0.0:6112), turns it off and uninstalls, checking the cleanup.

  Radmin VPN's adapter also uses metric 1; the script offers to disable it
  for the run and turns it back on afterwards.

  The selftest binds 0.0.0.0:6112 and :4445, which would raise a Windows
  Firewall prompt; the script adds a temporary allow rule for that one test
  binary and removes it again.

  Safe to re-run: install doubles as update, and anything left installed by
  an interrupted run is uninstalled at the end.

  Report: %TEMP%\toru-lan-selftest\report.txt and report.json.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\lan-selftest.ps1
#>
param(
  [switch]$Elevated,
  [switch]$KeepRadmin  # don't offer to disable Radmin VPN
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$out = Join-Path $env:TEMP 'toru-lan-selftest'
$payload = Join-Path $out 'payload'
$lanExe = Join-Path $payload 'toru-lan.exe'
$testExe = Join-Path $out 'lan-selftest.exe'
$tempRule = 'Toru LAN selftest (temporary)'

function Test-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  (New-Object Security.Principal.WindowsPrincipal $id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-Admin)) {
  # The one UAC prompt. The elevated copy does all the work and keeps its
  # window open; this one waits and prints the report.
  Write-Host 'Toru LAN selftest needs administrator rights (one UAC prompt).'
  $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-Elevated')
  if ($KeepRadmin) { $argList += '-KeepRadmin' }
  $p = Start-Process powershell.exe -Verb RunAs -ArgumentList $argList -Wait -PassThru
  $txt = Join-Path $out 'report.txt'
  if (Test-Path $txt) { Get-Content $txt }
  exit $p.ExitCode
}

New-Item -ItemType Directory -Force $payload | Out-Null
Start-Transcript -Path (Join-Path $out 'run.log') -Force | Out-Null
$disabledRadmin = @()
$exit = 1
try {
  Set-Location $repo

  $tagArg = @()
  $testTags = 'lanselftest'

  Write-Host 'Building toru-lan.exe'
  & go build @tagArg -ldflags '-X main.version=selftest' -o $lanExe ./cmd/toru-lan
  if ($LASTEXITCODE) { throw 'go build failed' }
  Copy-Item (Join-Path $repo 'build\windows\wintun\wintun.dll') $payload -Force
  Copy-Item (Join-Path $repo 'build\windows\wintun\LICENSE.txt') $payload -Force
  Write-Host 'Building the selftest'
  & go test -c -tags $testTags -o $testExe ./internal/lan/selftest
  if ($LASTEXITCODE) { throw 'go test -c failed' }

  # Radmin VPN also sets metric 1; with both up, which adapter Windows uses
  # for 255.255.255.255 is a coin toss.
  $radmin = Get-NetAdapter -ErrorAction SilentlyContinue |
    Where-Object { ($_.InterfaceDescription -like '*Radmin*' -or $_.Name -like '*Radmin*') -and $_.Status -eq 'Up' }
  if ($radmin) {
    Write-Warning ("Radmin VPN's adapter is up ({0}) and also uses metric 1." -f (($radmin | ForEach-Object Name) -join ', '))
    $answer = if ($KeepRadmin) { 'n' } else { Read-Host 'Disable it for this test and turn it back on afterwards? [Y/n]' }
    if ($answer -notmatch '^[nN]') {
      foreach ($a in $radmin) {
        Disable-NetAdapter -Name $a.Name -Confirm:$false
        $disabledRadmin += $a.Name
        Write-Host "Disabled $($a.Name) (will re-enable)"
      }
    }
  }

  # Allow just the test binary, so binding 0.0.0.0 raises no prompt.
  Get-NetFirewallRule -DisplayName $tempRule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  New-NetFirewallRule -DisplayName $tempRule -Direction Inbound -Action Allow -Program $testExe -Profile Any | Out-Null

  $env:TORU_LAN_SELFTEST_EXE = $lanExe
  $env:TORU_LAN_SELFTEST_OUT = $out
  Write-Host 'Running the selftest (about two minutes; the service idle stop takes 60 s)'
  # The test logs to stderr (wintun's driver log too). Under 'Stop',
  # Windows PowerShell turns each such line into a terminating error, so
  # relax it for this one call and pass the lines through as plain text.
  $ErrorActionPreference = 'Continue'
  # Quoted name=value flags: Windows PowerShell 5.1 splits a bare -test.v
  # into "-test" ".v" for native commands, and the test exe rejects -test.
  & $testExe '-test.v=true' '-test.run=^TestSelftest
    ForEach-Object { "$_" } | Tee-Object -FilePath (Join-Path $out 'test.log')
  $exit = $LASTEXITCODE
  $ErrorActionPreference = 'Stop'
}
catch {
  Write-Host "selftest script failed: $_" -ForegroundColor Red
  $exit = 1
}
finally {
  # Leave nothing behind, whatever happened above.
  if (Get-Service -Name ToruLan -ErrorAction SilentlyContinue) {
    Write-Host 'Toru LAN still installed: uninstalling'
    if (Test-Path $lanExe) { & $lanExe uninstall }
  }
  Get-NetFirewallRule -DisplayName $tempRule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  foreach ($name in $disabledRadmin) {
    Enable-NetAdapter -Name $name -Confirm:$false
    Write-Host "Re-enabled $name"
  }
  Stop-Transcript | Out-Null
}

$txt = Join-Path $out 'report.txt'
if (-not (Test-Path $txt)) {
  Write-Host 'No report was written: the selftest did not run. See test.log.' -ForegroundColor Red
  if ($exit -eq 0) { $exit = 1 }
}
if (Test-Path $txt) {
  Write-Host ''
  Get-Content $txt
}
Write-Host ''
Write-Host "Report: $txt (and report.json, test.log, run.log)"
if ($Elevated) { Read-Host 'Press Enter to close' | Out-Null }
exit $exit
 '-test.timeout=15m' 2>&1 |
    ForEach-Object { "$_" } | Tee-Object -FilePath (Join-Path $out 'test.log')
  $exit = $LASTEXITCODE
  $ErrorActionPreference = 'Stop'
}
catch {
  Write-Host "selftest script failed: $_" -ForegroundColor Red
  $exit = 1
}
finally {
  # Leave nothing behind, whatever happened above.
  if (Get-Service -Name ToruLan -ErrorAction SilentlyContinue) {
    Write-Host 'Toru LAN still installed: uninstalling'
    if (Test-Path $lanExe) { & $lanExe uninstall }
  }
  Get-NetFirewallRule -DisplayName $tempRule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  foreach ($name in $disabledRadmin) {
    Enable-NetAdapter -Name $name -Confirm:$false
    Write-Host "Re-enabled $name"
  }
  Stop-Transcript | Out-Null
}

$txt = Join-Path $out 'report.txt'
if (Test-Path $txt) {
  Write-Host ''
  Get-Content $txt
}
Write-Host ''
Write-Host "Report: $txt (and report.json, test.log, run.log)"
if ($Elevated) { Read-Host 'Press Enter to close' | Out-Null }
exit $exit
