#Requires -Version 5.1
param(
    [string]$Addr = "127.0.0.1:8765",
    [string]$HomeDir = "",
    [switch]$RealProvider,
    [switch]$Mock,
    [switch]$NoBuild
)

$ErrorActionPreference = "Stop"

function Get-ListenPort {
    param([string]$Address)

    if ($Address -notmatch ":(\d+)$") {
        throw "Addr must include a port, got '$Address'. Example: 127.0.0.1:8765"
    }
    return [int]$Matches[1]
}

function Get-GatewayURL {
    param(
        [string]$Address,
        [int]$Port
    )

    if ($Address -match "^(0\.0\.0\.0|\[?::\]?):") {
        return "http://127.0.0.1:$Port"
    }
    return "http://$Address"
}

$scriptRoot = Split-Path -Parent $PSCommandPath
Set-Location $scriptRoot

if ($RealProvider -and $Mock) {
    throw "Use either -RealProvider or -Mock, not both."
}

$devArgs = @("-Background", "-Addr", $Addr)
if (-not [string]::IsNullOrWhiteSpace($HomeDir)) {
    $devArgs += @("-HomeDir", $HomeDir)
}
if ($Mock) {
    $devArgs += "-Mock"
} else {
    $devArgs += "-RealProvider"
}
if ($NoBuild) {
    $devArgs += "-NoBuild"
}

& (Join-Path $scriptRoot "dev.ps1") @devArgs
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$port = Get-ListenPort -Address $Addr
$gatewayURL = Get-GatewayURL -Address $Addr -Port $port
$binary = Join-Path $scriptRoot "bin\fast-agent-harness.exe"

if (-not $Mock) {
    $destAuth = Join-Path $HOME "billyharness\auth\codex.json"
    if (-not [string]::IsNullOrWhiteSpace($HomeDir)) {
        $destAuth = Join-Path $HomeDir "auth\codex.json"
    }
    $sourceAuth = if ($env:CODEX_HOME) { Join-Path $env:CODEX_HOME "auth.json" } else { Join-Path $HOME ".codex\auth.json" }
    if ((Test-Path -LiteralPath $sourceAuth) -and -not (Test-Path -LiteralPath $destAuth)) {
        Write-Host ""
        Write-Host "Codex login exists, but Billyharness auth is not imported yet."
        Write-Host "In the TUI, type: /auth codex"
        Write-Host ""
    }
}

& $binary tui -gateway $gatewayURL
exit $LASTEXITCODE
