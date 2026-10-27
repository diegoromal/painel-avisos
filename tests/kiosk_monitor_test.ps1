# Testes da seleção de monitor e do lançador do quiosque (install-tv-kiosk.ps1), com mocks da API do Windows.
# Roda no pwsh (Linux/macOS/Windows) e no Windows PowerShell 5.1:  pwsh -NoProfile -File tests/kiosk_monitor_test.ps1
# Não abre Edge nem mexe em janelas: as chamadas ao Windows (user32/CIM) são substituídas por funções falsas.
$ErrorActionPreference = 'Stop'
$installer = Join-Path $PSScriptRoot '..\deploy\windows\install-tv-kiosk.ps1'
if (-not (Test-Path $installer)) { $installer = Join-Path $PSScriptRoot '../deploy/windows/install-tv-kiosk.ps1' }
$src = Get-Content $installer -Raw -Encoding UTF8
$script:pass = 0; $script:fail = 0
function Check([string]$name, [bool]$ok, [string]$detail = '') {
  if ($ok) { $script:pass++; Write-Host "PASS $name$(if ($detail) { " — $detail" })" } else { $script:fail++; Write-Host "FAIL $name$(if ($detail) { " — $detail" })" }
}
# --- biblioteca embutida no instalador ---
$tag = "`$MonitorLib = @'"
$a = $src.IndexOf($tag); $b = $src.IndexOf("`n'@", $a)
$MonitorLib = $src.Substring($a + $tag.Length, $b - $a - $tag.Length).Trim("`r", "`n")
. ([ScriptBlock]::Create($MonitorLib))
$csOpts = @{}; if ($PSVersionTable.PSVersion.Major -ge 7) { $csOpts.CompilerOptions = '-langversion:5' }
try { Add-Type -TypeDefinition $PainelMonCs -Language CSharp @csOpts; Check 'C# da biblioteca compila (C# 5, mesmo nível do Windows PowerShell 5.1)' $true }
catch { Check 'C# da biblioteca compila (C# 5)' $false $_.Exception.Message }

# --- ordem e numeração ---
$raw = @(
  [pscustomobject]@{ Device = '\\.\DISPLAY10'; Name = 'Samsung TV'; Primary = $false; X = 3840; Y = 0; Width = 1920; Height = 1080; Dpi = 96 },
  [pscustomobject]@{ Device = '\\.\DISPLAY2'; Name = 'Dell P2419H'; Primary = $false; X = -2880; Y = -200; Width = 2880; Height = 1620; Dpi = 144 },
  [pscustomobject]@{ Device = '\\.\DISPLAY1'; Name = 'Generic PnP'; Primary = $true; X = 0; Y = 0; Width = 1920; Height = 1080; Dpi = 120 }
)
$mons = @(ConvertTo-PainelMonitorList $raw)
Check 'Lista numerada por DISPLAYn (numérico: DISPLAY10 depois de DISPLAY2)' (($mons | ForEach-Object { "$($_.Index)=$($_.Device -replace '^\\\\\.\\','')" }) -join ' ' -eq '1=DISPLAY1 2=DISPLAY2 3=DISPLAY10') (($mons | ForEach-Object { "$($_.Index)=$($_.Device)" }) -join ' ')
Check 'Escala calculada do DPI (144 dpi = 150%, 120 = 125%)' ($mons[1].Scale -eq 150 -and $mons[0].Scale -eq 125)

# --- seleção ---
$s = Select-PainelMonitor $mons ''; Check 'Sem -Monitor = principal (comportamento antigo)' ($s.Monitor.Device -eq '\\.\DISPLAY1' -and -not $s.Fallback)
$s = Select-PainelMonitor $mons '0'; Check '-Monitor 0 = principal' ($s.Monitor.Primary)
$s = Select-PainelMonitor $mons 'principal'; Check '-Monitor principal' ($s.Monitor.Primary)
$s = Select-PainelMonitor $mons '2'; Check '-Monitor 2 = DISPLAY2 (à esquerda/acima, coordenadas negativas)' ($s.Monitor.Device -eq '\\.\DISPLAY2' -and $s.Monitor.X -eq -2880 -and $s.Monitor.Y -eq -200) $s.Message
$s = Select-PainelMonitor $mons '3'; Check '-Monitor 3 = DISPLAY10' ($s.Monitor.Device -eq '\\.\DISPLAY10')
$s = Select-PainelMonitor $mons 'DISPLAY10'; Check '-Monitor DISPLAY10 (nome do dispositivo)' ($s.Monitor.Index -eq 3)
$s = Select-PainelMonitor $mons '\\.\display2'; Check '-Monitor \\.\display2 (formato completo, maiúsc./minúsc.)' ($s.Monitor.Index -eq 2)
$s = Select-PainelMonitor $mons '5'; Check 'Monitor inexistente → principal com aviso (Fallback)' ($s.Fallback -and $s.Monitor.Primary -and $s.Message -match 'não encontrado') $s.Message
$s = Select-PainelMonitor $mons 'DISPLAY7'; Check 'Dispositivo inexistente → principal com aviso' ($s.Fallback -and $s.Monitor.Primary)
$two = @(ConvertTo-PainelMonitorList ($raw | Where-Object { $_.Device -ne '\\.\DISPLAY10' }))
$s = Select-PainelMonitor $two '3'; Check 'Monitor 3 desligado (só 2 conectados) → principal' ($s.Fallback -and $s.Monitor.Primary)
$noPrim = @(ConvertTo-PainelMonitorList ($raw | ForEach-Object { $c = $_.PSObject.Copy(); $c.Primary = $false; $c }))
$s = Select-PainelMonitor $noPrim '9'; Check 'Sem flag de principal → usa o 1º da lista' ($s.Monitor.Index -eq 1)
$s = Select-PainelMonitor @() '2'; Check 'Nenhum monitor lido → Monitor vazio (abre sem ajuste)' ($null -eq $s.Monitor -and $s.Fallback)
$okArgs = @('', '0', '1', '3', '12', 'principal', 'DISPLAY3', '\\.\DISPLAY12', 'display2') | Where-Object { -not (Test-PainelValidMonitorArg $_) }
$badArgs = @('-1', 'abc', '1,2', '100', 'DISPLAY', 'C:\x', "2; calc") | Where-Object { Test-PainelValidMonitorArg $_ }
Check 'Validação de -Monitor (aceita/recusa)' (-not $okArgs -and -not $badArgs) "aceitos indevidamente: $($badArgs -join ','); recusados indevidamente: $($okArgs -join ',')"

# --- argumentos do Edge ---
$ea = Get-PainelEdgePlacementArgs $mons[1]
Check '--window-position/--window-size com coordenadas negativas' (($ea -join ' ') -eq '--window-position=-2880,-200 --window-size=2880,1620') ($ea -join ' ')
Check 'Sem monitor → nenhum argumento extra' (@(Get-PainelEdgePlacementArgs $null).Count -eq 0)

# --- cobertura do retângulo ---
$m2 = $mons[1]
Check 'Janela cobrindo o monitor (tolerância 2 px)' (Test-PainelRectOnMonitor @(-2881, -199, -1, 1420) $m2)
Check 'Janela no monitor errado não conta' (-not (Test-PainelRectOnMonitor @(0, 0, 1920, 1080) $m2))
Check 'Janela no monitor certo mas tamanho de outro DPI não conta' (-not (Test-PainelRectOnMonitor @(-2880, -200, -960, 880) $m2))

# --- perfis e lançadores: uma tela não pode casar com outra ---
$pTv = 'C:\Users\TV User\AppData\Local\PainelAvisos\edge-kiosk-tv'
$pTv2 = 'C:\Users\TV User\AppData\Local\PainelAvisos\edge-kiosk-tv-2'
$cmdBrowserTv = '"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --kiosk https://x/tela?screen=tv --user-data-dir="' + $pTv + '" --no-first-run'
$cmdRendererTv2 = '"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" --type=renderer --user-data-dir="' + $pTv2 + '" --lang=pt-BR'
$cmdUnquoted = 'msedge.exe --type=gpu-process --user-data-dir=C:\Kiosk\edge-kiosk-tv --field-trial'
Check 'Perfil tv casa com o próprio Edge' (Test-PainelProfileMatch $cmdBrowserTv $pTv)
Check 'Perfil tv NÃO casa com o Edge da tela tv-2 (bug de prefixo da 1.1.2 corrigido)' (-not (Test-PainelProfileMatch $cmdRendererTv2 $pTv))
Check 'Perfil tv-2 casa com o próprio processo filho' (Test-PainelProfileMatch $cmdRendererTv2 $pTv2)
Check 'Perfil sem aspas e maiúsc./minúsc. diferentes' (Test-PainelProfileMatch $cmdUnquoted 'c:\kiosk\EDGE-KIOSK-TV')
Check 'Edge normal (sem --user-data-dir) não casa' (-not (Test-PainelProfileMatch '"msedge.exe" --profile-directory=Default' $pTv))
$lTv = 'powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "C:\PainelAvisos\tv-kiosk-tv.ps1"'
$lTv2 = 'powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "C:\PainelAvisos\tv-kiosk-tv-2.ps1"'
$lOther = 'powershell.exe -File "D:\Outro\tv-kiosk-tv.ps1"'
Check 'Lançador tv casa com o próprio processo (inclusive o da 1.1.2)' ((Test-PainelLauncherMatch $lTv 'tv') -and (Test-PainelLauncherMatch $lOther 'tv'))
Check 'Reinstalar tv NÃO mata o lançador da tv-2' (-not (Test-PainelLauncherMatch $lTv2 'tv'))
Check 'Reinstalar tv-2 NÃO mata o lançador da tv' (-not (Test-PainelLauncherMatch $lTv 'tv-2'))

# --- posicionamento da janela (API do Windows simulada) ---
$script:win = $null; $script:moves = 0; $script:behavior = 'ok'
function Find-PainelKioskWindow([string]$ProfileDir) { if ($script:win) { return [IntPtr]42 } return $null }
function Get-PainelWindowRect($Hwnd) { return $script:win }
function Move-PainelWindow($Hwnd, $Mon, [bool]$RestoreFirst) {
  $script:moves++
  switch ($script:behavior) {
    'ok' { $script:win = @($Mon.X, $Mon.Y, ($Mon.X + $Mon.Width), ($Mon.Y + $Mon.Height)) }
    'dpi' {  # 1ª vez o Edge aplica o tamanho sugerido pelo WM_DPICHANGED (125% → 150%); da 2ª em diante fica exato
      if ($script:moves -eq 1) { $script:win = @($Mon.X, $Mon.Y, ($Mon.X + 2304), ($Mon.Y + 1296)) } else { $script:win = @($Mon.X, $Mon.Y, ($Mon.X + $Mon.Width), ($Mon.Y + $Mon.Height)) } }
    'stuck' { }
  }
}
$P = @{ ProfileDir = 'x'; Mon = $m2; TimeoutSec = 1; SettleMs = 0; RetryMs = 0 }
$script:win = @($m2.X, $m2.Y, ($m2.X + $m2.Width), ($m2.Y + $m2.Height)); $script:moves = 0; $script:behavior = 'ok'
$r = Set-PainelKioskPlacement @P; Check 'Edge já abriu no monitor certo (--window-position funcionou) → não mexe' ($r.Ok -and -not $r.Moved -and $script:moves -eq 0) $r.Message
$script:win = @(0, 0, 1920, 1080); $script:moves = 0; $script:behavior = 'ok'
$r = Set-PainelKioskPlacement @P; Check 'Edge abriu no principal → move para o monitor 2 (SetWindowPos)' ($r.Ok -and $r.Moved -and $script:moves -eq 1) $r.Message
$script:win = @(0, 0, 1920, 1080); $script:moves = 0; $script:behavior = 'dpi'
$r = Set-PainelKioskPlacement @P; Check 'Troca de DPI entre monitores: 2ª tentativa corrige o tamanho' ($r.Ok -and $script:moves -eq 2) $r.Message
$script:win = @(0, 0, 1920, 1080); $script:moves = 0; $script:behavior = 'stuck'
$r = Set-PainelKioskPlacement @P; Check 'Janela que não obedece → desiste após 4 tentativas e informa' (-not $r.Ok -and $script:moves -eq 4 -and $r.Message -match 'não consegui') $r.Message
$script:win = $null; $script:moves = 0
$t0 = Get-Date; $r = Set-PainelKioskPlacement @P; Check 'Janela do Edge não aparece → para no tempo limite' (-not $r.Ok -and ((Get-Date) - $t0).TotalSeconds -lt 3 -and $r.Message -match 'não encontrada') $r.Message

# --- lançador gerado: renderiza como o instalador faria, para 2 telas no mesmo PC ---
$hs = $src.Substring($src.IndexOf("@`"`n# Gerado"), $src.IndexOf("`"@ | Set-Content") - $src.IndexOf("@`"`n# Gerado") + 2)
function Render([string]$Screen, [string]$Monitor) {
  $TelaUrl = "https://avisos.example/tela?screen=$Screen&key=tk_TESTE123"; $edge = 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe'
  $WaitSeconds = 120; $Base = 'https://avisos.example'; $logFile = "C:\PainelAvisos\tv-kiosk-$Screen.log"
  $profileDir = "C:\Users\TV User\AppData\Local\PainelAvisos\edge-kiosk-$Screen"
  return (Invoke-Expression $hs)
}
$tmp = [IO.Path]::GetTempPath()
$L1 = Render 'tv' '1'; $L2 = Render 'tv-2' '3'
$f1 = Join-Path $tmp 'tv-kiosk-tv.ps1'; $f2 = Join-Path $tmp 'tv-kiosk-tv-2.ps1'
Set-Content $f1 $L1 -Encoding UTF8; Set-Content $f2 $L2 -Encoding UTF8
$e1 = $null; $ast1 = [System.Management.Automation.Language.Parser]::ParseFile($f1, [ref]$null, [ref]$e1)
$e2 = $null; [void][System.Management.Automation.Language.Parser]::ParseFile($f2, [ref]$null, [ref]$e2)
Check 'Lançadores gerados (tv/monitor 1 e tv-2/monitor 3) sem erro de sintaxe' ($e1.Count -eq 0 -and $e2.Count -eq 0) "$($e1.Count) / $($e2.Count) erros"
Check 'Mutex, perfil e log separados por tela' ($L1 -match "Kiosk-tv'\)" -and $L2 -match "Kiosk-tv-2'\)" -and $L1 -match 'edge-kiosk-tv"' -and $L2 -match 'edge-kiosk-tv-2"' -and $L1 -match 'tv-kiosk-tv\.log' -and $L2 -match 'tv-kiosk-tv-2\.log')
Check 'Lançador guarda o pedido (-Monitor) e não coordenadas fixas' ($L2 -match "monitorWanted = '3'" -and $L2 -notmatch 'window-position=-?\d')
Check 'Lançador contém a biblioteca de monitores' ($L1.Contains('public static class PainelMon') -and $L1.Contains('function Set-PainelKioskPlacement'))
Check 'Lançador sem resto de expansão na instalação (variáveis do lançador intactas)' ($L1.Contains('$sel = Resolve-KioskMonitor') -and $L1.Contains('Start-Process -FilePath $edge -ArgumentList ($edgeArgs + $extra)'))
$L0 = Render 'tv' ''
Check 'Sem -Monitor: lançador não move janela (comportamento da 1.1.2)' ($L0 -match "monitorWanted = ''   #")

# funções do lançador gerado, executadas com mocks
$funcs = $ast1.FindAll({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $false)
foreach ($fn in $funcs) { if ($fn.Name -in 'Write-PainelLog', 'Resolve-KioskMonitor', 'Test-KioskEdge') { . ([ScriptBlock]::Create($fn.Extent.Text)) } }
$logFile = Join-Path $tmp "kiosk-test-$PID.log"; Remove-Item $logFile -ErrorAction SilentlyContinue
function Get-PainelMonitors { $script:currentMons }
$script:currentMons = $mons; $monitorWanted = '3'
$sel = Resolve-KioskMonitor $false
Check 'Lançador resolve o monitor na hora (3 = DISPLAY10)' ($sel.Monitor.Device -eq '\\.\DISPLAY10' -and -not $sel.Fallback)
$script:currentMons = @(ConvertTo-PainelMonitorList ($raw | ForEach-Object { $c = $_.PSObject.Copy(); if ($c.Device -eq '\\.\DISPLAY10') { $c.Width = 3840; $c.Height = 2160 }; $c }))
$sel = Resolve-KioskMonitor $true
Check 'Resolução do monitor 3 mudou (4K) → usa o tamanho novo' ($sel.Monitor.Width -eq 3840 -and $sel.Monitor.Height -eq 2160)
$script:currentMons = $two
$sel = Resolve-KioskMonitor $true
$log = Get-Content $logFile -Raw -ErrorAction SilentlyContinue
Check 'Monitor 3 desligado → principal + linha no log' ($sel.Fallback -and $sel.Monitor.Primary -and $log -match 'não encontrado') (($log -split "`n" | Where-Object { $_ })[-1])
$monitorWanted = ''
Check 'Sem -Monitor → nada a resolver' ($null -eq (Resolve-KioskMonitor $false))
function Get-CimInstance { param($ClassName, $Filter, $ErrorAction)
  @([pscustomobject]@{ ProcessId = 11; CommandLine = $cmdRendererTv2 }, [pscustomobject]@{ ProcessId = 12; CommandLine = '"msedge.exe" --profile-directory=Default' }) }
$profileDir = $pTv
Check 'Test-KioskEdge da tela tv ignora o Edge da tv-2 (abre o seu)' (-not (Test-KioskEdge))
$profileDir = $pTv2
Check 'Test-KioskEdge da tela tv-2 vê o seu Edge' (Test-KioskEdge)
Remove-Item $logFile, $f1, $f2 -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "RESULTADO: $script:pass/$($script:pass + $script:fail) verificações OK"
if ($script:fail) { exit 1 }
