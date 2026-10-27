<#
.SYNOPSIS  Remove o servidor do Painel de Avisos (mantém os dados, a menos que use -RemoveData).
#>
param([string]$InstallDir = "C:\PainelAvisos", [string]$DataDir = "C:\ProgramData\PainelAvisos", [switch]$RemoveData)
$ErrorActionPreference = "Continue"
Stop-ScheduledTask -TaskName "PainelAvisos" -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "PainelAvisos" -Confirm:$false -ErrorAction SilentlyContinue
Get-Process painel-avisos -ErrorAction SilentlyContinue | Stop-Process -Force
Get-NetFirewallRule -DisplayName "Painel de Avisos*" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
Remove-Item -Force (Join-Path $InstallDir "painel-avisos.exe") -ErrorAction SilentlyContinue
if ($RemoveData) { Remove-Item -Recurse -Force $DataDir -ErrorAction SilentlyContinue; Write-Host "Dados removidos." }
else { Write-Host "Dados mantidos em $DataDir (use -RemoveData para apagar)." }
Write-Host "Servidor removido."
