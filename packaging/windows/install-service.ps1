#Requires -RunAsAdministrator
param(
    [string]$InstallDir = 'C:\Program Files\Nexspence',
    [string]$DataDir    = 'C:\ProgramData\Nexspence',
    [string]$Source     = (Join-Path $PSScriptRoot '..\..')
)
$ErrorActionPreference = 'Stop'

$ConfigPath = Join-Path $DataDir 'config.yaml'
$Exe        = Join-Path $InstallDir 'nexspence.exe'

# Install binary
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item (Join-Path $Source 'nexspence.exe') $Exe -Force

# Data directory
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir 'data') | Out-Null

# Grant the low-privilege service account modify rights on the data + config tree
icacls $DataDir /grant "NT AUTHORITY\LocalService:(OI)(CI)M" /T | Out-Null

# Seed config from the example if none exists yet
if (-not (Test-Path $ConfigPath)) {
    Copy-Item (Join-Path $Source 'config.yaml.example') $ConfigPath
    # The Windows service has no working directory, so the example's relative
    # ./data/blobs would resolve under System32. Pin it to the data dir we created.
    (Get-Content $ConfigPath) -replace '\./data/blobs', 'C:/ProgramData/Nexspence/data/blobs' |
        Set-Content $ConfigPath
    Write-Host "Created $ConfigPath - edit database.dsn, auth.jwt_secret, bootstrap.admin_password before starting."
}

# Service binary path: quoted exe + args. New-Service passes this straight to the
# Win32 CreateService API, avoiding PowerShell native-argument quoting pitfalls
# (sc.exe would word-split a path containing spaces like 'C:\Program Files\...').
$binPath = "`"$Exe`" serve --config `"$ConfigPath`""

# Idempotent: if the service already exists, stop and remove it before recreating
# so a re-run picks up the new binary path.
$existing = Get-Service -Name nexspence -ErrorAction SilentlyContinue
if ($existing) {
    if ($existing.Status -ne 'Stopped') { Stop-Service nexspence -Force }
    sc.exe delete nexspence | Out-Null
    Start-Sleep -Seconds 1
}

# Run as the low-privilege LocalService virtual account (no password).
$cred = New-Object System.Management.Automation.PSCredential(
    'NT AUTHORITY\LocalService',
    (New-Object System.Security.SecureString))

New-Service -Name nexspence `
    -BinaryPathName $binPath `
    -DisplayName 'Nexspence' `
    -StartupType Manual `
    -Credential $cred | Out-Null

# Description is set separately (New-Service has no -Description on Windows PowerShell 5.1).
sc.exe description nexspence "Nexspence artifact repository manager" | Out-Null

Write-Host ""
Write-Host "Service 'nexspence' registered. After editing $ConfigPath, run:"
Write-Host "    Start-Service nexspence"
