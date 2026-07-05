#Requires -Version 5.1
param(
    [string]$Addr = "127.0.0.1:8765",
    [string]$HomeDir = "",
    [switch]$RealProvider,
    [switch]$NoBuild,
    [switch]$KillExisting,
    [switch]$NoDevLoopback,
    [switch]$NewWindow,
    [switch]$Child
)

$ErrorActionPreference = "Stop"

function Get-ListenPort {
    param([string]$Address)

    if ($Address -notmatch ":(\d+)$") {
        throw "Addr must include a port, got '$Address'. Example: 127.0.0.1:8765"
    }
    return [int]$Matches[1]
}

function Get-Listeners {
    param([int]$Port)

    try {
        $connections = @(Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
    } catch {
        return @()
    }

    $items = @()
    foreach ($conn in $connections) {
        $name = "unknown"
        try {
            $proc = Get-Process -Id $conn.OwningProcess -ErrorAction Stop
            $name = $proc.ProcessName
        } catch {
        }
        $items += [pscustomobject]@{
            PID          = $conn.OwningProcess
            LocalAddress = $conn.LocalAddress
            LocalPort    = $conn.LocalPort
            ProcessName  = $name
        }
    }

    return $items | Sort-Object PID, LocalAddress -Unique
}

function Stop-ExistingBillyHarness {
    param([object[]]$Listeners)

    $foreign = @($Listeners | Where-Object { $_.ProcessName -ne "fast-agent-harness" })
    if ($foreign.Count -gt 0) {
        Write-Host "Port is used by a non-billyharness process:"
        $foreign | Format-Table -AutoSize | Out-String | Write-Host
        throw "Refusing to stop a non-billyharness process. Use -Addr with another port."
    }

    $pids = @($Listeners | Select-Object -ExpandProperty PID -Unique)
    foreach ($pidValue in $pids) {
        Write-Host "Stopping existing fast-agent-harness.exe PID $pidValue..."
        Stop-Process -Id $pidValue -Force -ErrorAction Stop
    }
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

function Write-TUICommand {
    param([string]$GatewayURL)

    Write-Host "TUI command:"
    Write-Host "  .\bin\fast-agent-harness.exe tui -gateway $GatewayURL"
}

function Quote-Arg {
    param([string]$Value)

    if ($Value -match '[\s"]') {
        return '"' + ($Value -replace '"', '\"') + '"'
    }
    return $Value
}

function Get-CurrentPowerShell {
    try {
        $proc = Get-Process -Id $PID -ErrorAction Stop
        if ($proc.Path) {
            return $proc.Path
        }
    } catch {
    }

    $pwsh = Get-Command pwsh -ErrorAction SilentlyContinue
    if ($pwsh) {
        return $pwsh.Source
    }

    $powershell = Get-Command powershell -ErrorAction Stop
    return $powershell.Source
}

$scriptRoot = Split-Path -Parent $PSCommandPath
Set-Location $scriptRoot

if ([string]::IsNullOrWhiteSpace($HomeDir)) {
    if ([string]::IsNullOrWhiteSpace($env:BILLYHARNESS_HOME)) {
        $env:BILLYHARNESS_HOME = Join-Path $HOME "billyharness"
    }
} else {
    $env:BILLYHARNESS_HOME = $HomeDir
}
New-Item -ItemType Directory -Force -Path $env:BILLYHARNESS_HOME | Out-Null

$port = Get-ListenPort -Address $Addr
$binary = Join-Path $scriptRoot "bin\fast-agent-harness.exe"
$binaryDir = Split-Path -Parent $binary
New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null

if (-not $NoBuild) {
    Write-Host "Building $binary..."
    & go build -buildvcs=false -o $binary .\cmd\fast-agent-harness
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }
} elseif (-not (Test-Path -LiteralPath $binary)) {
    throw "Binary not found at $binary. Run without -NoBuild first."
}

$listeners = @(Get-Listeners -Port $port)
if ($listeners.Count -gt 0) {
    if ($KillExisting) {
        Stop-ExistingBillyHarness -Listeners $listeners
        Start-Sleep -Milliseconds 300
        $listeners = @(Get-Listeners -Port $port)
    }

    if ($listeners.Count -gt 0) {
        $nonBilly = @($listeners | Where-Object { $_.ProcessName -ne "fast-agent-harness" })
        if ($nonBilly.Count -eq 0) {
            $gatewayURL = Get-GatewayURL -Address $Addr -Port $port
            Write-Host "Gateway is already running on port ${port}:"
            $listeners | Format-Table -AutoSize | Out-String | Write-Host
            Write-Host "  URL:  $gatewayURL"
            Write-Host "  Home: $env:BILLYHARNESS_HOME"
            Write-Host ""
            Write-TUICommand -GatewayURL $gatewayURL
            Write-Host ""
            Write-Host "Restart it with: .\dev.ps1 -KillExisting"
            return
        }

        Write-Host "Port $port is already in use:"
        $listeners | Format-Table -AutoSize | Out-String | Write-Host
        Write-Host "Use another port: .\dev.ps1 -Addr 127.0.0.1:9876"
        Write-Host "Or stop an existing Billyharness gateway: .\dev.ps1 -KillExisting"
        throw "Gateway port is busy."
    }
}

if ($NewWindow -and -not $Child) {
    $psExe = Get-CurrentPowerShell
    $childArgs = @(
        "-NoExit",
        "-ExecutionPolicy", "Bypass",
        "-File", $PSCommandPath,
        "-Child",
        "-NoBuild",
        "-Addr", $Addr,
        "-HomeDir", $env:BILLYHARNESS_HOME
    )
    if ($RealProvider) {
        $childArgs += "-RealProvider"
    }
    if ($NoDevLoopback) {
        $childArgs += "-NoDevLoopback"
    }

    $argLine = ($childArgs | ForEach-Object { Quote-Arg $_ }) -join " "
    Start-Process -FilePath $psExe -ArgumentList $argLine -WorkingDirectory $scriptRoot
    Write-Host "Opened gateway window for http://$Addr"
    return
}

$gatewayArgs = @("gateway", "-addr", $Addr)
if (-not $RealProvider) {
    $gatewayArgs += "-mock"
}
if (-not $NoDevLoopback) {
    $gatewayArgs += "-dev-allow-unauthenticated-loopback-mutations"
}

$gatewayURL = Get-GatewayURL -Address $Addr -Port $port
$providerLabel = if ($RealProvider) { "configured provider" } else { "mock" }

Write-Host ""
Write-Host "Billyharness gateway"
Write-Host "  URL:      $gatewayURL"
Write-Host "  Home:     $env:BILLYHARNESS_HOME"
Write-Host "  Provider: $providerLabel"
Write-Host ""
Write-TUICommand -GatewayURL $gatewayURL
Write-Host ""

& $binary @gatewayArgs
if ($LASTEXITCODE -ne 0) {
    throw "Gateway exited with code $LASTEXITCODE"
}
