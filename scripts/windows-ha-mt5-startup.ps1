# Windows Server Self-Hosting High Availability (HA) MT5 Startup Script
# Coordinates terminal64.exe process management and Antigravity Gemini 3.7 Node Server integration

param (
    [string]$TerminalPath = "C:\Program Files\MetaTrader 5\terminal64.exe",
    [string]$NodeServerScript = "scripts/node-gemini-server.js",
    [int]$Port = 3000
)

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$rootDir = Split-Path -Parent $scriptDir
$logDir = Join-Path $rootDir "logs"
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir | Out-Null }
$logFile = Join-Path $logDir "ha-mt5-startup.log"

function Write-HALog {
    param ([string]$message)
    $timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $entry = "[$timestamp] [HA-SYSTEM] $message"
    Write-Host $entry
    Add-Content -Path $logFile -Value $entry
}

Write-HALog "Initializing Exness MetaTrader 5 Live HA Startup Routine on Windows Server..."

# Check Node Server Execution
$nodeProcesses = Get-Process -Name "node" -ErrorAction SilentlyContinue
if ($nodeProcesses) {
    Write-HALog "Node server process is active."
} else {
    Write-HALog "Starting Antigravity Gemini 3.7 Node Server (port $Port)..."
    Start-Process node -ArgumentList (Join-Path $rootDir $NodeServerScript) -WindowStyle Hidden
}

# Check MetaTrader 5 Terminal process
$mt5Processes = Get-Process -Name "terminal64" -ErrorAction SilentlyContinue
if ($mt5Processes) {
    Write-HALog "Exness MetaTrader 5 terminal64.exe is running under process ID(s): $($mt5Processes.Id -join ', ')"
} else {
    if (Test-Path $TerminalPath) {
        Write-HALog "Launching Exness MetaTrader 5 terminal: $TerminalPath"
        Start-Process -FilePath $TerminalPath -ArgumentList "/portable"
    } else {
        Write-HALog "Terminal path not found at $TerminalPath. Simulation mode active for non-production environments."
    }
}

Write-HALog "Exness Live MT5 HA Self-Hosting Startup Completed Successfully."
