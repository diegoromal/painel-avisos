<#
.SYNOPSIS
  Instala o servidor do Painel de Avisos no Windows (roda sozinho ao ligar o PC).

.DESCRIPTION
  - Copia painel-avisos.exe para C:\PainelAvisos
  - Cria a pasta de dados em C:\ProgramData\PainelAvisos (banco, imagens, log) com acesso restrito
  - No primeiro uso, cria o usuário "admin" e MOSTRA A SENHA na tela (anote!)
  - Libera a porta no Firewall do Windows (somente redes Privada/Domínio, a menos que use -AllProfiles)
  - Registra uma Tarefa Agendada "PainelAvisos" que inicia no boot como SYSTEM e reinicia se cair
  - Desativa suspensão/hibernação na tomada (o servidor precisa ficar ligado), a menos que use -SkipPowerSettings

.EXAMPLE
  # PowerShell como Administrador, dentro da pasta deploy\windows:
  Set-ExecutionPolicy -Scope Process Bypass -Force
  .\install-server.ps1
  .\install-server.ps1 -Port 8080 -AllProfiles
#>
[CmdletBinding()]
param(
  [int]$Port = 8080,
  [string]$InstallDir = "C:\PainelAvisos",
  [string]$DataDir = "C:\ProgramData\PainelAvisos",
  [string]$ExePath = "",
  [switch]$AllProfiles,
  [switch]$SkipPowerSettings,
  # exige chave em TODAS as telas (desliga o modo LAN legado sem chave)
  [switch]$RequireScreenKeys
)
$ErrorActionPreference = "Stop"
$TaskName = "PainelAvisos"

function Assert-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Execute este script em um PowerShell aberto como Administrador."
  }
}
Assert-Admin

# 1) localizar o executável
if (-not $ExePath) {
  $candidates = @(
    (Join-Path $PSScriptRoot "painel-avisos.exe"),
    (Join-Path $PSScriptRoot "..\..\dist\painel-avisos.exe"),
    (Join-Path $PSScriptRoot "..\..\painel-avisos.exe")
  )
  $ExePath = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $ExePath -or -not (Test-Path $ExePath)) { throw "painel-avisos.exe não encontrado. Use -ExePath C:\caminho\painel-avisos.exe" }
Write-Host "==> Executável: $ExePath" -ForegroundColor Cyan

# 2) parar instância anterior (atualização)
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  Write-Host "==> Parando instalação anterior..."
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Start-Sleep -Seconds 2
}
Get-Process painel-avisos -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

# 3) copiar binário
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$Exe = Join-Path $InstallDir "painel-avisos.exe"
Copy-Item -Force $ExePath $Exe
Write-Host "==> Copiado para $Exe"

# 4) pasta de dados com acesso só para SYSTEM e Administradores
$firstRun = -not (Test-Path (Join-Path $DataDir "painel.db"))
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
& icacls $DataDir /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null

# 5) primeiro uso: criar admin e mostrar a senha
if ($firstRun) {
  Write-Host ""
  Write-Host "==> Primeiro uso: criando usuário administrador" -ForegroundColor Yellow
  & $Exe reset-admin -data $DataDir
  Write-Host ""
  Write-Host "ANOTE A SENHA ACIMA. Ela não fica salva em texto em lugar nenhum." -ForegroundColor Yellow
  Write-Host "(Para redefinir depois: `"$Exe`" reset-admin -data `"$DataDir`")"
  Write-Host ""
}

# 6) firewall
$ruleName = "Painel de Avisos (TCP $Port)"
Get-NetFirewallRule -DisplayName "Painel de Avisos*" -ErrorAction SilentlyContinue | Remove-NetFirewallRule
$profiles = if ($AllProfiles) { "Any" } else { "Domain,Private" }
New-NetFirewallRule -DisplayName $ruleName -Direction Inbound -Protocol TCP -LocalPort $Port -Action Allow -Profile $profiles | Out-Null
Write-Host "==> Firewall: porta $Port liberada (perfis: $profiles)"
$public = Get-NetConnectionProfile -ErrorAction SilentlyContinue | Where-Object { $_.NetworkCategory -eq "Public" }
if ($public -and -not $AllProfiles) {
  Write-Warning ("A rede '{0}' está como PÚBLICA; a regra não vale nela. Mude para Privada " -f ($public[0].Name)) 
  Write-Warning "(Set-NetConnectionProfile -InterfaceAlias '<adaptador>' -NetworkCategory Private) ou rode de novo com -AllProfiles."
}

# 7) tarefa agendada: no boot, como SYSTEM, reinicia se cair
$taskArgs  = "-port $Port -data `"$DataDir`"" + $(if ($RequireScreenKeys) { " -allow-keyless 0" } else { "" })
$action    = New-ScheduledTaskAction -Execute $Exe -Argument $taskArgs -WorkingDirectory $InstallDir
$trigger   = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
$settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable `
               -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings `
  -Description "Painel de Avisos - servidor HTTP na porta $Port" -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "==> Tarefa agendada '$TaskName' registrada (inicia no boot como SYSTEM) e iniciada"

# 8) energia
if (-not $SkipPowerSettings) {
  powercfg /change standby-timeout-ac 0 | Out-Null
  powercfg /change hibernate-timeout-ac 0 | Out-Null
  Write-Host "==> Suspensão/hibernação na tomada desativadas (o servidor precisa ficar ligado)"
}

# 9) verificar
$ok = $false
foreach ($i in 1..15) {
  try { $r = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 2; if ($r.ok) { $ok = $true; break } } catch {}
  Start-Sleep -Seconds 1
}
if (-not $ok) { Write-Warning "O servidor não respondeu em http://127.0.0.1:$Port/healthz. Veja o log em $DataDir\painel-avisos.log"; exit 1 }

$ips = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
  Where-Object { $_.IPAddress -notlike "127.*" -and $_.IPAddress -notlike "169.254.*" } | Select-Object -ExpandProperty IPAddress
Write-Host ""
Write-Host "Servidor no ar!" -ForegroundColor Green
foreach ($ip in $ips) {
  Write-Host ("  Líder (publicar avisos): http://{0}:{1}/admin" -f $ip, $Port)
  Write-Host ("  TV (tela cheia):         http://{0}:{1}/tela?screen=<nome>&key=<chave>  (chave: Painel > Telas)" -f $ip, $Port)
}
Write-Host "  Log: $DataDir\painel-avisos.log"
