#Requires -RunAsAdministrator
$ErrorActionPreference = 'Continue'
sc.exe stop nexspence | Out-Null
sc.exe delete nexspence | Out-Null
Write-Host "Service 'nexspence' removed. Data in C:\ProgramData\Nexspence and the binary in C:\Program Files\Nexspence were left in place."
