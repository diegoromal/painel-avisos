# Testes portáveis, sem acessar Windows, LSA, tarefas ou processos reais.
$ErrorActionPreference = 'Stop'
$src = Get-Content (Join-Path $PSScriptRoot '../deploy/windows/install-tv-kiosk.ps1') -Raw -Encoding UTF8
$script:pass = 0; $script:fail = 0
function Check([string]$name, [bool]$ok, [string]$detail = '') {
  if ($ok) { $script:pass++; Write-Host "PASS $name" } else { $script:fail++; Write-Host "FAIL $name $detail" }
}
function Throws($Action) { try { & $Action; return $false } catch { return $true } }
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($src, [ref]$null, [ref]$errors)
Check 'Parse do instalador' ($errors.Count -eq 0) ($errors -join ';')
# Carrega somente funções e os dois here-strings de bibliotecas, nunca o fluxo de instalação.
foreach ($var in 'MonitorLib','PainelLsaCs') {
  $assignment = $ast.FindAll({ param($n) $n -is [Management.Automation.Language.AssignmentStatementAst] }, $true) | Where-Object { $_.Left.Extent.Text -eq ('$' + $var) } | Select-Object -First 1
  . ([scriptblock]::Create($assignment.Extent.Text))
}
. ([scriptblock]::Create($MonitorLib))
foreach ($fn in $ast.FindAll({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] }, $false)) { . ([scriptblock]::Create($fn.Extent.Text)) }
$opts = @{}; if ($PSVersionTable.PSVersion.Major -ge 7) { $opts.CompilerOptions = '-langversion:5' }
try { Add-Type -TypeDefinition $PainelLsaCs -Language CSharp @opts; Check 'LSA compila em C# 5' $true } catch { Check 'LSA compila em C# 5' $false $_ }
$hs = $src.Substring($src.IndexOf("@`"`n# Gerado"), $src.IndexOf("`"@ | Set-Content") - $src.IndexOf("@`"`n# Gerado") + 2)
function Render([string]$Monitor, [bool]$AllUsers) {
  $Screen = 'tv'; $TelaUrl = 'https://example.test/tela?screen=tv&key=tk_TESTE123'
  $edge = 'C:\Program Files\Edge\msedge.exe'; $WaitSeconds = 120; $Base = 'https://example.test'
  $profileDir = 'C:\INSTALLER_PRIVATE\profile'; $logFile = 'C:\INSTALLER_PRIVATE\log'
  Invoke-Expression $hs
}
foreach ($case in @(@('', $false), @('3', $false), @('2', $true))) {
  $launcher = Render $case[0] $case[1]; $errs = $null
  [void][Management.Automation.Language.Parser]::ParseInput($launcher, [ref]$null, [ref]$errs)
  Check "Parse lançador Monitor=$($case[0]) AllUsers=$($case[1])" ($errs.Count -eq 0)
  Check 'Perfil e log resolvidos em runtime' ($launcher.Contains('$profileDir = Join-Path $env:LOCALAPPDATA "PainelAvisos\edge-kiosk-tv"') -and $launcher.Contains('$logFile = Join-Path $env:LOCALAPPDATA "PainelAvisos\tv-kiosk-tv.log"') -and -not $launcher.Contains('INSTALLER_PRIVATE'))
  Check 'Início e mutex antes de healthz' ($launcher.IndexOf("Write-PainelLog 'lançador iniciado'") -lt $launcher.IndexOf('while ((Get-Date) -lt $deadline)') -and $launcher.IndexOf('$mutex.WaitOne(0)') -lt $launcher.IndexOf("Write-PainelLog 'lançador iniciado'"))
  Check 'Espera periódica, sucesso, timeout e duplicado registrados' ($launcher.Contains('AddSeconds(30)') -and $launcher.Contains('servidor respondeu') -and $launcher.Contains('tempo de espera esgotado') -and $launcher.Contains('já existe outro lançador desta tela; saindo'))
  Check 'Argumento do perfil usa variável runtime' ($launcher.Contains('"--user-data-dir=`"$profileDir`""'))
}
function Get-PainelCurrentUser { 'DOMINIO\TV User' }
foreach ($all in $false,$true) {
  $d = New-PainelTaskDefinition 'tv-2' 'C:\Painel & TV\tv-kiosk-tv-2.ps1' 'DOMINIO\TV & User' $all
  [xml]$xml = ConvertTo-PainelTaskXml $d
  $ns = New-Object Xml.XmlNamespaceManager($xml.NameTable); $ns.AddNamespace('t', $xml.DocumentElement.NamespaceURI)
  function Node($path) { $xml.SelectSingleNode('//t:' + ($path -replace '/', '/t:'), $ns).InnerText }
  Check 'Nome da tarefa isolado por tela' ($d.Name -eq 'PainelAvisos-Kiosk-tv-2')
  Check 'LogonTrigger e atraso 20s' ((Node 'LogonTrigger/Delay') -eq 'PT20S')
  if ($all) { Check 'Grupo Users sem UserId no gatilho' ((Node 'Principal/GroupId') -eq 'S-1-5-32-545' -and -not $xml.SelectSingleNode('//t:LogonTrigger/t:UserId', $ns)) }
  else { Check 'Usuário e token interativo' ((Node 'Principal/LogonType') -eq 'InteractiveToken' -and (Node 'LogonTrigger/UserId') -eq 'DOMINIO\TV & User') }
  Check 'Privilégios limitados' ((Node 'Principal/RunLevel') -eq 'LeastPrivilege')
  Check 'Reinício 999 vezes por minuto' ((Node 'RestartOnFailure/Count') -eq '999' -and (Node 'RestartOnFailure/Interval') -eq 'PT1M')
  Check 'Sem limite de execução' ((Node 'Settings/ExecutionTimeLimit') -eq 'PT0S')
  Check 'Bateria e disponibilidade' ((Node 'Settings/DisallowStartIfOnBatteries') -eq 'false' -and (Node 'Settings/StopIfGoingOnBatteries') -eq 'false' -and (Node 'Settings/StartWhenAvailable') -eq 'true')
  Check 'IgnoreNew' ((Node 'Settings/MultipleInstancesPolicy') -eq 'IgnoreNew')
  Check 'Ação e escape XML preservam caminho' ((Node 'Exec/Command') -eq 'powershell.exe' -and (Node 'Exec/Arguments') -eq '-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "C:\Painel & TV\tv-kiosk-tv-2.ps1"')
  $acl = Get-PainelAclArgs 'C:\tv.ps1' $all
  Check 'ACL por escopo' (($acl -contains '*S-1-5-32-545:(RX)') -eq $all -and $acl -contains 'DOMINIO\TV User:(M)')
}
foreach ($bad in @(@{AutoLogon=$true;DisableAutoLogon=$true}, @{AutoLogon=$true;Status=$true}, @{Uninstall=$true;ListMonitors=$true}, @{AutoLogon=$true;Uninstall=$true}, @{DisableAutoLogon=$true;ListMonitors=$true}, @{AutoLogonUser='x'}, @{Status=$true;Url='https://x'})) { Check 'Combinação inválida rejeitada' (Throws { Assert-PainelOptions $bad }) }
foreach ($good in @(@{}, @{AutoLogon=$true}, @{AutoLogon=$true;Url='https://x'}, @{Status=$true}, @{Uninstall=$true}, @{ListMonitors=$true})) { Check 'Combinação válida aceita' (-not (Throws { Assert-PainelOptions $good })) }
Check 'Resultados traduzidos' ((Get-PainelTaskResult 0) -eq 'sucesso' -and (Get-PainelTaskResult 267011) -eq 'nunca executou' -and (Get-PainelTaskResult 267009) -eq 'em execução' -and (Get-PainelTaskResult 5) -match 'erro')
# Registro e LSA simulados; senha jamais vai para streams de saída.
$script:registry = @{}; $script:lsaFail = $false; $script:deleted = $false; $script:prompted = $false
function Test-PainelAdmin { $false }
Check 'AutoLogon sem administrador falha antes de pedir senha' (Throws { Set-PainelAutoLogon 'D\tv' $null })
Check 'DisableAutoLogon sem administrador falha' (Throws { Clear-PainelAutoLogon })
function Test-PainelAdmin { $true }
function Set-ItemProperty { param($Path,$Name,$Value) $script:registry[$Name] = $Value }
function Remove-ItemProperty { param($Path,$Name,$ErrorAction) $script:registry.Remove($Name) }
function Invoke-PainelLsa { param($Password,[switch]$Delete,[switch]$Query) if ($script:lsaFail) { throw 'LSA mock' }; if ($Delete) { $script:deleted=$true }; if ($Query) { return $true } }
function Read-Host { param($Prompt,[switch]$AsSecureString) if (-not $AsSecureString) { throw 'Senha sem proteção' }; $script:prompted = $true; ConvertTo-SecureString 'SEGREDO_TESTE' -AsPlainText -Force }
$output = (& { Set-PainelAutoLogon 'D\tv' $null } *>&1 | Out-String)
Check 'Senha solicitada como SecureString e nunca exibida' ($script:prompted -and -not $output.Contains('SEGREDO_TESTE'))
Check 'LSA remove senha clara e configura usuário' (-not $script:registry.ContainsKey('DefaultPassword') -and $script:registry.AutoAdminLogon -eq '1' -and $script:registry.DefaultUserName -eq 'tv' -and $script:registry.DefaultDomainName -eq 'D')
$script:lsaFail = $true
$output = (& { Set-PainelAutoLogon 'D\tv' (ConvertTo-SecureString 'SEGREDO_TESTE' -AsPlainText -Force) } *>&1 | Out-String)
Check 'Fallback avisa texto claro sem exibir senha' ($output -match 'texto claro' -and -not $output.Contains('SEGREDO_TESTE') -and $script:registry.DefaultPassword -eq 'SEGREDO_TESTE')
$script:lsaFail = $false
Clear-PainelAutoLogon
Check 'Disable remove registro e segredo' ($script:deleted -and $script:registry.AutoAdminLogon -eq '0' -and -not $script:registry.ContainsKey('DefaultPassword'))
function Get-ItemProperty { param($Path,$ErrorAction) [pscustomobject]$script:registry }
$state = Get-PainelAutoLogonState
Check 'Estado real do registro e LSA' (-not $state.Enabled -and $state.LsaSecret -eq $true -and -not $state.PlainPassword)
$output = (& { Show-PainelAutoLogon } 6>&1 | Out-String)
Check 'ATENÇÃO e comando quando desativado' ($output -match 'ATENÇÃO: sem logon automático o painel só abre depois que alguém entrar no Windows' -and $output -match '\-AutoLogon')
$script:registry.AutoAdminLogon='1'
$output = (& { Show-PainelAutoLogon } 6>&1 | Out-String)
Check 'Usuário informado quando ativo' ($output -match 'Logon automático ativo: D\\tv')
# Desinstalação: nada real é encerrado/removido.
$env:LOCALAPPDATA = '/tmp/painel-autostart-user'
$script:stopped=@(); $script:removed=@(); $script:tasks=@()
function Get-CimInstance { param($ClassName,$Filter,$ErrorAction)
  if ($Filter -match 'powershell') { @([pscustomobject]@{ProcessId=101;CommandLine='powershell.exe -File "C:\PainelAvisos\tv-kiosk-tv.ps1"'}, [pscustomobject]@{ProcessId=102;CommandLine='powershell.exe -File "C:\PainelAvisos\tv-kiosk-tv-2.ps1"'}) }
  else { @([pscustomobject]@{ProcessId=201;CommandLine=('msedge.exe --user-data-dir="' + (Join-Path $env:LOCALAPPDATA 'PainelAvisos\edge-kiosk-tv') + '"')}, [pscustomobject]@{ProcessId=202;CommandLine=('msedge.exe --user-data-dir="' + (Join-Path $env:LOCALAPPDATA 'PainelAvisos\edge-kiosk-tv-2') + '"')}) }
}
function Stop-Process { param($Id,[switch]$Force,$ErrorAction) $script:stopped += $Id }
function Get-ScheduledTask { param($TaskName,$ErrorAction) [pscustomobject]@{State='Ready'} }
function Unregister-ScheduledTask { param($TaskName,$Confirm,$ErrorAction) $script:tasks += $TaskName }
function Get-PainelOldLinks($ScreenName) { "C:\Startup\Painel de Avisos - $ScreenName.lnk"; "C:\CommonStartup\Painel de Avisos - $ScreenName.lnk" }
function Test-Path { param($Path) $true }
function Remove-Item { param($Path,[switch]$Force,$ErrorAction) $script:removed += $Path }
Remove-PainelScreen 'tv' '/tmp/painel-install'
Check 'Uninstall encerra só lançador e Edge da tela' ($script:stopped.Count -eq 2 -and $script:stopped -contains 101 -and $script:stopped -contains 201)
Check 'Uninstall encerra o Edge da tela também de outro usuário (perfil em outro LOCALAPPDATA)' ('msedge.exe --user-data-dir="C:\Users\Outro\AppData\Local\PainelAvisos\edge-kiosk-tv" --kiosk' -match ('--user-data-dir="?[^"]*[\\/]PainelAvisos[\\/]edge-kiosk-' + [regex]::Escape('tv') + '"?(\s|$)')) -and -not ('msedge.exe --user-data-dir="C:\Users\Outro\AppData\Local\PainelAvisos\edge-kiosk-tv-2" --kiosk' -match ('--user-data-dir="?[^"]*[\\/]PainelAvisos[\\/]edge-kiosk-' + [regex]::Escape('tv') + '"?(\s|$)'))
Check 'Uninstall remove só tarefa da tela' ($script:tasks.Count -eq 1 -and $script:tasks[0] -eq 'PainelAvisos-Kiosk-tv')
Check 'Uninstall remove lançador, logs e dois atalhos sem tocar tv-2' ($script:removed.Count -eq 5 -and -not ($script:removed -match 'tv-2') -and ($script:removed -match 'tv-kiosk-tv.ps1'))
function Get-ScheduledTaskInfo { param($TaskName) [pscustomobject]@{LastRunTime=[datetime]'2026-10-01';LastTaskResult=267009} }
$script:tail = 0
function Get-Content { param($Path,$Tail) $script:tail=$Tail; 'linha do log simulada' }
$output = (& { Show-PainelStatus 'tv' '/tmp/painel-install' } 6>&1 | Out-String)
Check 'Status mostra tarefa, resultado e processos' ($output -match 'PainelAvisos-Kiosk-tv' -and $output -match 'em execução' -and $output -match 'Lançador rodando: True; Edge do usuário atual: True')
Check 'Status lê últimas 20 linhas do log do usuário atual' ($script:tail -eq 20 -and $output.Contains((Join-Path $env:LOCALAPPDATA 'PainelAvisos\tv-kiosk-tv.log')) -and $output -match 'linha do log simulada')
Check 'Status não remove nem encerra nada' ($script:removed.Count -eq 5 -and $script:stopped.Count -eq 2 -and $script:tasks.Count -eq 1)
$script:lsaFail = $true
$state = Get-PainelAutoLogonState
Check 'Consulta LSA sem permissão é diagnosticada sem falhar' ($state.LsaSecret -match 'indisponível')
$script:lsaFail = $false
Set-PainelAutoLogon '' (ConvertTo-SecureString 'SEGREDO_TESTE' -AsPlainText -Force)
Check 'AutoLogon usa usuário atual por padrão' ($script:registry.DefaultDomainName -eq 'DOMINIO' -and $script:registry.DefaultUserName -eq 'TV User')
Check 'Registro da tarefa usa XML e Force; início respeita NoStart' ($src.Contains('Register-ScheduledTask -TaskName $definition.Name -Xml (ConvertTo-PainelTaskXml $definition) -Force') -and $src.Contains('if (-not $NoStart) { Start-ScheduledTask'))
Write-Host ""
Write-Host "RESULTADO: $script:pass/$($script:pass + $script:fail) verificações OK"
if ($script:fail) { exit 1 }
