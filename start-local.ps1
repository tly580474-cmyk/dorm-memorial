[CmdletBinding()]
param(
    [switch]$NoBrowser
)

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

$runtimeDir = Join-Path $PSScriptRoot 'data\local-runtime'
$logDir = Join-Path $runtimeDir 'logs'
$buildDir = Join-Path $PSScriptRoot 'build'
New-Item -ItemType Directory -Force -Path $runtimeDir, $logDir, $buildDir | Out-Null

function Get-ListenerProcess([int]$Port) {
    $listener = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if (-not $listener) {
        return $null
    }
    return Get-Process -Id $listener.OwningProcess -ErrorAction SilentlyContinue
}

function Wait-Endpoint([string]$Name, [string]$Url, [int]$Attempts = 60) {
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 2 | Out-Null
            Write-Host "[ready] $Name -> $Url" -ForegroundColor Green
            return
        } catch {
            Start-Sleep -Milliseconds 500
        }
    }
    throw "$Name did not become ready: $Url"
}

function Wait-Listener([string]$Name, [int]$Port, [int]$Attempts = 60) {
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) {
            Write-Host "[ready] $Name -> 127.0.0.1:$Port" -ForegroundColor Green
            return
        }
        Start-Sleep -Milliseconds 500
    }
    throw "$Name did not begin listening on port $Port"
}

function Start-LoggedProcess(
    [string]$Name,
    [string]$FilePath,
    [string[]]$Arguments,
    [string]$WorkingDirectory
) {
    $stdout = Join-Path $logDir "$Name.stdout.log"
    $stderr = Join-Path $logDir "$Name.stderr.log"
    $startParameters = @{
        FilePath = $FilePath
        WorkingDirectory = $WorkingDirectory
        WindowStyle = 'Hidden'
        RedirectStandardOutput = $stdout
        RedirectStandardError = $stderr
        PassThru = $true
    }
    if ($Arguments.Count -gt 0) {
        $startParameters.ArgumentList = $Arguments
    }
    $process = Start-Process @startParameters
    Write-Host "[start] $Name (PID $($process.Id))"
    return $process
}

$alistExe = Join-Path $PSScriptRoot 'alist.exe'
if (-not (Test-Path -LiteralPath $alistExe)) {
    throw "AList executable not found: $alistExe"
}
$goExe = (Get-Command go.exe -ErrorAction Stop).Source
$npmExe = (Get-Command npm.cmd -ErrorAction Stop).Source
$ffmpegExe = (Get-Command ffmpeg.exe -ErrorAction Stop).Source
$env:APP_FFMPEG_PATH = $ffmpegExe
$backendExe = Join-Path $buildDir 'dorm-memorial-local.exe'
$caddyConfig = Join-Path $PSScriptRoot 'Caddyfile'
$caddyCommand = Get-Command caddy.exe -ErrorAction SilentlyContinue
if ($caddyCommand) {
    $caddyExe = $caddyCommand.Source
} else {
    $wingetPackageDir = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
    $caddyExe = Get-ChildItem -LiteralPath $wingetPackageDir -Filter caddy.exe -File -Recurse -ErrorAction SilentlyContinue |
        Where-Object FullName -Like '*CaddyServer.Caddy*' |
        Sort-Object LastWriteTime -Descending |
        Select-Object -First 1 -ExpandProperty FullName
}
if (-not $caddyExe -or -not (Test-Path -LiteralPath $caddyExe)) {
    throw 'Caddy executable not found. Install Caddy with: winget install --exact --id CaddyServer.Caddy'
}
if (-not (Test-Path -LiteralPath $caddyConfig)) {
    throw "Caddy configuration not found: $caddyConfig"
}

Write-Host '[build] Vue frontend'
& $npmExe run build --prefix web
if ($LASTEXITCODE -ne 0) {
    throw 'Vue frontend build failed'
}

Write-Host '[build] Go backend'
& $goExe build -trimpath -o $backendExe .\cmd\server
if ($LASTEXITCODE -ne 0) {
    throw 'Go backend build failed'
}

$processes = [ordered]@{}

$alistProcess = Get-ListenerProcess 5244
if ($alistProcess) {
    Write-Host "[reuse] AList (PID $($alistProcess.Id))"
} else {
    $alistProcess = Start-LoggedProcess 'alist' $alistExe @('server') $PSScriptRoot
}
$processes.alist = $alistProcess.Id
Wait-Endpoint 'AList' 'http://127.0.0.1:5244/api/public/settings'

$backendProcess = Get-ListenerProcess 13048
if ($backendProcess) {
    Write-Host "[reuse] application (PID $($backendProcess.Id))"
} else {
    $backendProcess = Start-LoggedProcess 'backend' $backendExe @() $PSScriptRoot
}
$processes.backend = $backendProcess.Id
Wait-Endpoint 'backend' 'http://127.0.0.1:13048/health/ready'
Wait-Endpoint 'frontend' 'http://127.0.0.1:13048/'

$caddyProcess = Get-ListenerProcess 13443
if ($caddyProcess) {
    if ($caddyProcess.ProcessName -ne 'caddy') {
        throw "Port 13443 is already used by $($caddyProcess.ProcessName) (PID $($caddyProcess.Id))"
    }
    Write-Host "[reuse] Caddy (PID $($caddyProcess.Id))"
} else {
    $caddyProcess = Start-LoggedProcess 'caddy' $caddyExe @('run', '--config', $caddyConfig) $PSScriptRoot
}
$processes.caddy = $caddyProcess.Id
Wait-Listener 'Caddy HTTPS' 13443

$state = [ordered]@{
    started_at = (Get-Date).ToUniversalTime().ToString('o')
    alist_pid = $processes.alist
    backend_pid = $processes.backend
    frontend_pid = $processes.backend
    caddy_pid = $processes.caddy
    frontend_url = 'http://127.0.0.1:13048/'
    backend_url = 'http://127.0.0.1:13048/'
    https_url = 'https://m3048.clical.xin/'
    alist_url = 'http://127.0.0.1:5244/'
}
$state | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $runtimeDir 'services.json') -Encoding UTF8

Write-Host ''
Write-Host 'All local services are ready.' -ForegroundColor Green
Write-Host 'Frontend: http://127.0.0.1:13048/'
Write-Host 'Backend:  http://127.0.0.1:13048/'
Write-Host 'AList:    http://127.0.0.1:5244/'
Write-Host 'HTTPS:    https://m3048.clical.xin/ (requires FRP)'
Write-Host "Logs:     $logDir"

if (-not $NoBrowser) {
    Start-Process 'http://127.0.0.1:13048/'
}
