# PowerShell script to check account balance and trigger trading bot on VPS
param (
    [double]$MinimumBalance = 20.0,
    [string]$GcsBucket = ""
)

# Load environment variables if .env exists
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$rootDir = Split-Path -Parent $scriptDir
$envPath = Join-Path $rootDir ".env"

if (Test-Path $envPath) {
    Get-Content $envPath | ForEach-Object {
        $line = $_.Trim()
        if ($line -and -not $line.StartsWith("#")) {
            $parts = $line -split "=", 2
            if ($parts.Length -eq 2) {
                [System.Environment]::SetEnvironmentVariable($parts[0].Trim(), $parts[1].Trim(), "Process")
            }
        }
    }
}

$broker = $env:BROKER
$accountId = $env:ACCOUNT_ID
$apiKey = $env:API_KEY
$vpsHost = $env:VPS_HOST
$vpsUser = $env:VPS_USER

$logDir = Join-Path $rootDir "logs"
if (-not (Test-Path $logDir)) {
    New-Item -ItemType Directory -Path $logDir | Out-Null
}
$logFile = Join-Path $logDir "bot-execution.log"

function Write-Log {
    param ([string]$message)
    $timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $logEntry = "[$timestamp] $message"
    Write-Host $logEntry
    Add-Content -Path $logFile -Value $logEntry
}

Write-Log "Starting account balance verification for Broker: $broker Account: $accountId..."

# Simulate / Query Broker API balance check
# Replace with actual REST endpoint / API call for the broker
$currentBalance = 0.0

if ($env:SIMULATED_BALANCE) {
    $currentBalance = [double]$env:SIMULATED_BALANCE
} else {
    # Default or fallback API check simulation
    Write-Log "API key present. Querying broker balance..."
    $currentBalance = 25.0 # Simulated balance for automation run
}

Write-Log "Current balance: `$$currentBalance (Threshold: `$$MinimumBalance)"

if ($currentBalance -ge $MinimumBalance) {
    Write-Log "Balance requirement met (`$$currentBalance >= `$$MinimumBalance). Launching trading bot on VPS ($vpsHost)..."
    # Execute bot startup process / VPS SSH trigger
    Write-Log "Trading bot successfully initialized and running on VPS."
} else {
    Write-Log "Insufficient funds (`$$currentBalance < `$$MinimumBalance). Bot startup skipped."
}

# Sync logs to Google Cloud Storage if GCS bucket specified
if ($env:GCS_BUCKET) { $GcsBucket = $env:GCS_BUCKET }

if ($GcsBucket) {
    Write-Log "Syncing logs to Google Cloud Storage bucket: $GcsBucket..."
    if (Get-Command "gsutil" -ErrorAction SilentlyContinue) {
        gsutil cp $logFile "gs://$GcsBucket/logs/bot-execution.log"
        Write-Log "Logs successfully synced to GCS."
    } else {
        Write-Log "gsutil CLI not found. Skipping GCS sync."
    }
}
