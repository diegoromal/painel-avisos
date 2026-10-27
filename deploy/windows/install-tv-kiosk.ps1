<#
.SYNOPSIS
  Configura o PC da TV para abrir o Painel de Avisos em tela cheia (modo quiosque do Edge) ao fazer logon.

.DESCRIPTION
  - Cria C:\PainelAvisos\tv-kiosk-<tela>.ps1: espera o servidor responder, abre o Edge em quiosque e o reabre se for fechado
  - Registra tarefa agendada interativa por tela no logon (usuário atual, ou todos com -AllUsers)
  - -AutoLogon configura logon automático como administrador; -DisableAutoLogon desativa
  - -Status diagnostica sem alterar; -Uninstall remove apenas a tela indicada
  - Usa um perfil separado do Edge por tela (não interfere no Edge normal nem nas outras telas) e libera o som automático
  - Com -Monitor, abre o quiosque num monitor específico (PC com vários monitores)
  - Desativa desligamento de tela, suspensão e proteção de tela, a menos que use -SkipPowerSettings

.PARAMETER Uninstall
  Remove tarefa, atalhos antigos, lançador e log apenas de -Screen. Mantém o logon automático.
.PARAMETER Status
  Mostra diagnóstico da tela e últimas 20 linhas do log do usuário atual, sem alterar nada.
.PARAMETER AutoLogon
  Configura logon automático (exige administrador). Pode ser usado sozinho ou com a instalação.
.PARAMETER DisableAutoLogon
  Desativa logon automático e remove a senha do registro e LSA (exige administrador).
.PARAMETER AutoLogonUser
  Usuário do logon automático, no formato DOMINIO\usuario; padrão: usuário atual.
.PARAMETER AutoLogonPassword
  Senha SecureString opcional. Quando omitida, solicita com Read-Host -AsSecureString.
.PARAMETER Monitor
  Monitor onde o quiosque abre. Vazio = monitor principal (padrão).
  Aceita o número da lista de -ListMonitors (1, 2, 3...: ordem DISPLAY1, DISPLAY2... do Windows)
  ou o nome do dispositivo (ex.: DISPLAY3). Se o monitor não existir na hora de abrir, usa o principal e registra no log.

.PARAMETER ListMonitors
  Lista os monitores (número, dispositivo, principal, resolução, posição, escala), mostra o número
  em cada tela por alguns segundos e sai. Use -NoIdentify para só listar.

.EXAMPLE
  # Servidor publicado na internet (HTTPS) — chave obrigatória:
  .\install-tv-kiosk.ps1 -Url https://avisos.protatic.qosit.cloud -Screen tv-principal -Key tk_XXXXXXXX
.EXAMPLE
  # PC com 3 monitores: descobrir o número de cada um e abrir no monitor 3
  .\install-tv-kiosk.ps1 -ListMonitors
  .\install-tv-kiosk.ps1 -Url https://avisos.protatic.qosit.cloud -Screen tv-principal -Key tk_XXXXXXXX -Monitor 3
.EXAMPLE
  # Servidor rodando no próprio PC da TV (LAN):
  .\install-tv-kiosk.ps1 -Server 127.0.0.1:8080 -Screen tv-principal -Key tk_XXXXXXXX
  # Servidor em outro PC da rede:
  .\install-tv-kiosk.ps1 -Server 192.168.1.195:8080 -Screen tv-refeitorio -Key tk_XXXXXXXX
.EXAMPLE
  .\install-tv-kiosk.ps1 -Url https://avisos.exemplo.com -Key tk_XXXXXXXX -AutoLogon
  .\install-tv-kiosk.ps1 -AutoLogon -AutoLogonUser DOMINIO\usuario
  .\install-tv-kiosk.ps1 -Status -Screen tv-principal
  .\install-tv-kiosk.ps1 -Uninstall -Screen tv-principal
  .\install-tv-kiosk.ps1 -DisableAutoLogon
.NOTES
  -AllUsers permite leitura da chave por usuários locais. Perfil e log são resolvidos por usuário.
  Logon automático usa segredo LSA; se indisponível, avisa e guarda senha em texto claro no registro.
  A chave (-Key) é mostrada uma única vez no painel (Telas > Gerar chave). Ela fica gravada no
  lançador C:\PainelAvisos\tv-kiosk-<tela>.ps1, com acesso restrito a este usuário, SYSTEM e Administradores.
  Sem -Key só funciona se o servidor estiver no modo LAN legado (telas sem chave).
  Duas telas no mesmo PC: rode uma vez para cada -Screen (com -Monitor diferentes). Cada tela tem seu
  lançador, tarefa, perfil do Edge e log (%LOCALAPPDATA%\PainelAvisos\tv-kiosk-<tela>.log); reinstalar uma não mexe na outra.
#>
[CmdletBinding()]
param(
  [string]$Server = "127.0.0.1:8080",
  [string]$Url = "",
  [string]$Screen = "tv-principal",
  [string]$Key = "",
  [string]$Monitor = "",
  [switch]$ListMonitors,
  [switch]$NoIdentify,
  [int]$WaitSeconds = 120,
  [string]$InstallDir = "C:\PainelAvisos",
  [switch]$AllUsers,
  [switch]$SkipPowerSettings,
  [switch]$NoStart,
  [switch]$Uninstall,
  [switch]$Status,
  [switch]$AutoLogon,
  [switch]$DisableAutoLogon,
  [string]$AutoLogonUser,
  [Security.SecureString]$AutoLogonPassword
)
$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------------------------
# Biblioteca de monitores/janelas. É usada aqui e copiada para dentro do lançador gerado, que
# resolve o monitor na hora de abrir o Edge (sobrevive a troca de resolução, ordem ou monitor desligado).
# Coordenadas: sempre em pixels FÍSICOS (a thread entra em modo DPI per-monitor v2 em cada chamada).
# ---------------------------------------------------------------------------------------------
$MonitorLib = @'
# ===== Painel de Avisos: monitores (gerado pelo install-tv-kiosk.ps1) =====
$PainelMonCs = '
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class PainelMon {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] public struct MONITORINFOEX {
    public int cbSize; public RECT rcMonitor; public RECT rcWork; public uint dwFlags;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string szDevice; }
  [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] public struct DISPLAY_DEVICE {
    public int cb;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)] public string DeviceName;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceString;
    public int StateFlags;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceID;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string DeviceKey; }
  public class MonInfo { public string Device; public string Name; public bool Primary; public int X; public int Y; public int Width; public int Height; public int Dpi; }
  public delegate bool MonitorEnumProc(IntPtr hMon, IntPtr hdc, ref RECT r, IntPtr data);
  public delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr data);
  [DllImport("user32.dll")] static extern bool EnumDisplayMonitors(IntPtr hdc, IntPtr clip, MonitorEnumProc cb, IntPtr data);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern bool GetMonitorInfo(IntPtr hMon, ref MONITORINFOEX mi);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern bool EnumDisplayDevices(string dev, uint i, ref DISPLAY_DEVICE dd, uint flags);
  [DllImport("user32.dll")] static extern IntPtr MonitorFromWindow(IntPtr hWnd, uint flags);
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumWindowsProc cb, IntPtr data);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hWnd);
  [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hWnd, out RECT r);
  [DllImport("user32.dll")] static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int w, int h, uint flags);
  [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr hWnd, int cmd);
  [DllImport("user32.dll")] static extern IntPtr GetWindow(IntPtr hWnd, uint cmd);
  [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(IntPtr hWnd, StringBuilder sb, int max);
  [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr ctx);
  [DllImport("user32.dll")] static extern bool SetProcessDpiAwarenessContext(IntPtr ctx);
  [DllImport("shcore.dll")] static extern int SetProcessDpiAwareness(int v);
  [DllImport("user32.dll")] static extern bool SetProcessDPIAware();
  [DllImport("shcore.dll")] static extern int GetDpiForMonitor(IntPtr hMon, int type, out uint x, out uint y);

  static string mode = "";
  public static string DpiMode { get { return mode; } }
  // processo inteiro DPI-aware (melhor esforço; pode já ter sido definido) ...
  public static void EnableProcessDpi() {
    try { if (SetProcessDpiAwarenessContext(new IntPtr(-4))) { mode = "processo per-monitor-v2"; return; } } catch (Exception) { }
    try { if (SetProcessDpiAwareness(2) == 0) { mode = "processo per-monitor"; return; } } catch (Exception) { }
    try { SetProcessDPIAware(); mode = "processo system-aware"; } catch (Exception) { }
  }
  // ... e cada chamada roda com a thread em per-monitor v2 (Windows 10 1703+), o que garante pixels físicos
  static IntPtr Enter() {
    IntPtr old = IntPtr.Zero;
    try { old = SetThreadDpiAwarenessContext(new IntPtr(-4)); if (old == IntPtr.Zero) old = SetThreadDpiAwarenessContext(new IntPtr(-3)); }
    catch (Exception) { old = IntPtr.Zero; }
    if (old != IntPtr.Zero && mode.IndexOf("thread") < 0) mode = (mode == "" ? "" : mode + " + ") + "thread per-monitor";
    return old;
  }
  static void Leave(IntPtr old) { if (old != IntPtr.Zero) { try { SetThreadDpiAwarenessContext(old); } catch (Exception) { } } }

  public static List<MonInfo> GetMonitors() {
    List<MonInfo> list = new List<MonInfo>();
    IntPtr old = Enter();
    try {
      EnumDisplayMonitors(IntPtr.Zero, IntPtr.Zero, delegate(IntPtr h, IntPtr hdc, ref RECT r, IntPtr d) {
        MONITORINFOEX mi = new MONITORINFOEX(); mi.cbSize = Marshal.SizeOf(typeof(MONITORINFOEX));
        if (GetMonitorInfo(h, ref mi)) {
          MonInfo m = new MonInfo();
          m.Device = mi.szDevice; m.Primary = (mi.dwFlags & 1) != 0;
          m.X = mi.rcMonitor.Left; m.Y = mi.rcMonitor.Top;
          m.Width = mi.rcMonitor.Right - mi.rcMonitor.Left; m.Height = mi.rcMonitor.Bottom - mi.rcMonitor.Top;
          m.Dpi = 96;
          try { uint dx, dy; if (GetDpiForMonitor(h, 0, out dx, out dy) == 0) m.Dpi = (int)dx; } catch (Exception) { }
          DISPLAY_DEVICE dd = new DISPLAY_DEVICE(); dd.cb = Marshal.SizeOf(typeof(DISPLAY_DEVICE));
          m.Name = EnumDisplayDevices(mi.szDevice, 0, ref dd, 0) ? dd.DeviceString : "";
          list.Add(m);
        }
        return true;
      }, IntPtr.Zero);
    } finally { Leave(old); }
    return list;
  }

  // janelas principais (visíveis, sem dono, classe Chrome_WidgetWin_*) dos processos informados, maior primeiro
  public static IntPtr[] FindWindows(uint[] pids) {
    Dictionary<uint, bool> set = new Dictionary<uint, bool>(); foreach (uint p in pids) set[p] = true;  // só mscorlib (sem System.Core)
    List<KeyValuePair<long, IntPtr>> found = new List<KeyValuePair<long, IntPtr>>();
    IntPtr old = Enter();
    try {
      EnumWindows(delegate(IntPtr h, IntPtr d) {
        uint pid; GetWindowThreadProcessId(h, out pid);
        if (!set.ContainsKey(pid) || !IsWindowVisible(h) || GetWindow(h, 4) != IntPtr.Zero) return true;
        StringBuilder sb = new StringBuilder(256); GetClassName(h, sb, 256);
        if (!sb.ToString().StartsWith("Chrome_WidgetWin")) return true;
        RECT r; if (!GetWindowRect(h, out r)) return true;
        long area = (long)(r.Right - r.Left) * (long)(r.Bottom - r.Top);
        if (area >= 40000) found.Add(new KeyValuePair<long, IntPtr>(area, h));
        return true;
      }, IntPtr.Zero);
    } finally { Leave(old); }
    found.Sort(delegate(KeyValuePair<long, IntPtr> a, KeyValuePair<long, IntPtr> b) { return b.Key.CompareTo(a.Key); });
    IntPtr[] res = new IntPtr[found.Count];
    for (int i = 0; i < found.Count; i++) res[i] = found[i].Value;
    return res;
  }

  public static int[] GetRect(IntPtr h) {
    IntPtr old = Enter();
    try { RECT r; if (!GetWindowRect(h, out r)) return null; return new int[] { r.Left, r.Top, r.Right, r.Bottom }; }
    finally { Leave(old); }
  }

  public static string MonitorOf(IntPtr h) {
    IntPtr old = Enter();
    try {
      IntPtr m = MonitorFromWindow(h, 2);
      MONITORINFOEX mi = new MONITORINFOEX(); mi.cbSize = Marshal.SizeOf(typeof(MONITORINFOEX));
      return GetMonitorInfo(m, ref mi) ? mi.szDevice : "";
    } finally { Leave(old); }
  }

  // move/redimensiona a janela para cobrir exatamente o monitor (pixels físicos)
  public static bool Place(IntPtr h, int x, int y, int w, int hh, bool restoreFirst) {
    IntPtr old = Enter();
    try {
      if (restoreFirst) ShowWindow(h, 9);               // SW_RESTORE
      return SetWindowPos(h, IntPtr.Zero, x, y, w, hh, 0x0020 | 0x0040); // HWND_TOP, SWP_FRAMECHANGED | SWP_SHOWWINDOW
    } finally { Leave(old); }
  }
}
'
function Initialize-PainelMon {
  if (-not ('PainelMon' -as [type])) {
    Add-Type -TypeDefinition $PainelMonCs -Language CSharp
    [PainelMon]::EnableProcessDpi()
  }
}
function Get-PainelDisplayNumber([string]$Device) {
  if ($Device -match 'DISPLAY(\d+)') { return [int]$Matches[1] }
  return 9999
}
# ordena por DISPLAY1, DISPLAY2... (numérico: DISPLAY10 vem depois de DISPLAY2) e numera a partir de 1
function ConvertTo-PainelMonitorList($Raw) {
  $i = 0
  @($Raw) | Where-Object { $_ } | Sort-Object @{ Expression = { Get-PainelDisplayNumber $_.Device } }, @{ Expression = { $_.Device } } | ForEach-Object {
    $i++
    [pscustomobject]@{
      Index = $i; Device = [string]$_.Device; Name = [string]$_.Name; Primary = [bool]$_.Primary
      X = [int]$_.X; Y = [int]$_.Y; Width = [int]$_.Width; Height = [int]$_.Height
      Scale = [int][Math]::Round(([double]$(if ($_.Dpi) { $_.Dpi } else { 96 })) * 100 / 96)
    }
  }
}
function Get-PainelMonitors {
  Initialize-PainelMon
  ConvertTo-PainelMonitorList ([PainelMon]::GetMonitors())
}
# -Monitor: '' / 0 / principal = principal; número = posição na lista; DISPLAYn ou \\.\DISPLAYn = dispositivo
function Select-PainelMonitor($Monitors, [string]$Wanted) {
  $list = @($Monitors | Where-Object { $_ })
  $primary = $list | Where-Object { $_.Primary } | Select-Object -First 1
  if (-not $primary) { $primary = $list | Select-Object -First 1 }
  $w = "$Wanted".Trim()
  if ($w -eq '' -or $w -eq '0' -or $w -match '^(principal|primary)$') {
    return [pscustomobject]@{ Monitor = $primary; Fallback = $false; Message = "monitor principal" }
  }
  $m = $null
  if ($w -match '^\d+$') {
    $m = $list | Where-Object { $_.Index -eq [int]$w } | Select-Object -First 1
    $desc = "monitor $w"
  } else {
    $name = ($w -replace '^\\\\\.\\', '')
    $m = $list | Where-Object { ($_.Device -replace '^\\\\\.\\', '') -ieq $name } | Select-Object -First 1
    $desc = "monitor $name"
  }
  if ($m) { return [pscustomobject]@{ Monitor = $m; Fallback = $false; Message = "$desc = $($m.Device) $($m.Width)x$($m.Height) em $($m.X),$($m.Y) (escala $($m.Scale)%)" } }
  $pd = if ($primary) { $primary.Device } else { "(nenhum)" }
  return [pscustomobject]@{ Monitor = $primary; Fallback = $true; Message = "$desc não encontrado ($($list.Count) monitor(es) conectados); usando o principal $pd" }
}
function Test-PainelValidMonitorArg([string]$Wanted) {
  return ("$Wanted".Trim() -match '^(|0|[1-9]\d?|principal|primary|(\\\\\.\\)?DISPLAY\d{1,3})$')
}
# a janela cobre o monitor inteiro? (rect = Left,Top,Right,Bottom em pixels físicos)
function Test-PainelRectOnMonitor($Rect, $Mon, [int]$Tolerance = 2) {
  if (-not $Rect -or -not $Mon) { return $false }
  return ([Math]::Abs($Rect[0] - $Mon.X) -le $Tolerance -and [Math]::Abs($Rect[1] - $Mon.Y) -le $Tolerance -and
          [Math]::Abs($Rect[2] - ($Mon.X + $Mon.Width)) -le $Tolerance -and [Math]::Abs($Rect[3] - ($Mon.Y + $Mon.Height)) -le $Tolerance)
}
# dica de posição para o Edge (pode ser ignorada/reinterpretada em DIP pelo Edge; a correção vem depois)
function Get-PainelEdgePlacementArgs($Mon) {
  if (-not $Mon) { return @() }
  return @("--window-position=$($Mon.X),$($Mon.Y)", "--window-size=$($Mon.Width),$($Mon.Height)")
}
# processos do Edge deste perfil: o caminho tem de terminar ali (edge-kiosk-tv não pode casar com edge-kiosk-tv-2)
function Test-PainelProfileMatch([string]$CommandLine, [string]$ProfileDir) {
  if (-not $CommandLine) { return $false }
  return $CommandLine -match ('--user-data-dir="?' + [regex]::Escape($ProfileDir) + '"?(\s|$)')
}
function Test-PainelLauncherMatch([string]$CommandLine, [string]$ScreenName) {
  if (-not $CommandLine) { return $false }
  return $CommandLine -match ('\\tv-kiosk-' + [regex]::Escape($ScreenName) + '\.ps1"?(\s|$)')
}
function Get-PainelKioskEdge([string]$ProfileDir) {
  @(Get-CimInstance Win32_Process -Filter "Name='msedge.exe'" -ErrorAction SilentlyContinue |
    Where-Object { Test-PainelProfileMatch $_.CommandLine $ProfileDir })
}
# camadas finas sobre a API do Windows (substituíveis nos testes)
function Find-PainelKioskWindow([string]$ProfileDir) {
  $ids = [uint32[]]@(Get-PainelKioskEdge $ProfileDir | ForEach-Object { [uint32]$_.ProcessId })
  if (-not $ids.Count) { return $null }
  $w = [PainelMon]::FindWindows($ids)
  if ($w -and $w.Length) { return $w[0] }
  return $null
}
function Get-PainelWindowRect($Hwnd) { return [PainelMon]::GetRect($Hwnd) }
function Move-PainelWindow($Hwnd, $Mon, [bool]$RestoreFirst) { [void][PainelMon]::Place($Hwnd, $Mon.X, $Mon.Y, $Mon.Width, $Mon.Height, $RestoreFirst) }
# coloca a janela do quiosque cobrindo o monitor; devolve @{ Ok; Moved; Message }
function Set-PainelKioskPlacement([string]$ProfileDir, $Mon, [int]$TimeoutSec = 30, [int]$SettleMs = 2000, [int]$RetryMs = 1500) {
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  $h = $null
  do {
    $h = Find-PainelKioskWindow $ProfileDir
    if ($h) { break }
    Start-Sleep -Milliseconds 500
  } while ((Get-Date) -lt $deadline)
  if (-not $h) { return [pscustomobject]@{ Ok = $false; Moved = $false; Message = "janela do Edge do quiosque não encontrada em $TimeoutSec s" } }
  if ($SettleMs -gt 0) { Start-Sleep -Milliseconds $SettleMs }   # deixa o Edge entrar em tela cheia
  $moved = $false
  for ($try = 1; $try -le 4; $try++) {
    $r = Get-PainelWindowRect $h
    if (Test-PainelRectOnMonitor $r $Mon) {
      $how = if ($moved) { "movida para" } else { "já estava em" }
      return [pscustomobject]@{ Ok = $true; Moved = $moved; Message = "janela $how $($Mon.Device) ($($Mon.Width)x$($Mon.Height) em $($Mon.X),$($Mon.Y)) — tentativa $try" }
    }
    # 1ª/2ª: SetWindowPos direto (a 2ª corrige o tamanho que o Edge sugere ao trocar de DPI); 3ª: restaura e move
    Move-PainelWindow $h $Mon ($try -ge 3)
    $moved = $true
    Start-Sleep -Milliseconds $RetryMs
  }
  $r = Get-PainelWindowRect $h
  $rs = if ($r) { "$($r[0]),$($r[1])-$($r[2]),$($r[3])" } else { "?" }
  return [pscustomobject]@{ Ok = $false; Moved = $moved; Message = "não consegui cobrir $($Mon.Device) exatamente (janela em $rs)" }
}
# ===== fim da biblioteca de monitores =====
'@
. ([ScriptBlock]::Create($MonitorLib))

function Show-PainelMonitorIds($Mons, [int]$Seconds = 8) {
  try {
    Add-Type -AssemblyName System.Windows.Forms, System.Drawing
    $forms = foreach ($m in $Mons) {
      $f = New-Object System.Windows.Forms.Form
      $f.FormBorderStyle = 'None'; $f.TopMost = $true; $f.ShowInTaskbar = $false; $f.StartPosition = 'Manual'
      $f.BackColor = [System.Drawing.Color]::FromArgb(49, 143, 211); $f.ForeColor = [System.Drawing.Color]::White
      $f.Bounds = New-Object System.Drawing.Rectangle(($m.X + 60), ($m.Y + 60), 420, 300)
      $l = New-Object System.Windows.Forms.Label
      $l.Dock = 'Fill'; $l.TextAlign = 'MiddleCenter'; $l.Font = New-Object System.Drawing.Font("Segoe UI", 40, [System.Drawing.FontStyle]::Bold)
      $l.Text = "$($m.Index)" + $(if ($m.Primary) { "`nprincipal" } else { "" })
      $f.Controls.Add($l); $f.Show(); $f
    }
    $end = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $end) { [System.Windows.Forms.Application]::DoEvents(); Start-Sleep -Milliseconds 100 }
    $forms | ForEach-Object { $_.Close() }
  } catch { Write-Warning "Não foi possível mostrar o número nas telas: $($_.Exception.Message)" }
}

# Funções independentes da instalação: também usadas pelos testes com mocks.
function Test-PainelAdmin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  return (New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Assert-PainelOptions($Options) {
  $modes = @('Uninstall','Status','ListMonitors') | Where-Object { $Options[$_] }
  if ($modes.Count -gt 1 -or ($modes.Count -and ($Options.AutoLogon -or $Options.DisableAutoLogon)) -or ($Options.AutoLogon -and $Options.DisableAutoLogon)) { throw 'Combinação inválida: use -Uninstall, -Status, -ListMonitors e as opções de logon separadamente.' }
  if (($modes.Count -or $Options.DisableAutoLogon) -and ($Options.Url -or $Options.Key -or $Options.ContainsKey('Server'))) { throw 'Diagnóstico, remoção e desativação do logon não aceitam parâmetros de instalação (-Url/-Server/-Key).' }
  if (($Options.AutoLogonUser -or $Options.AutoLogonPassword) -and -not $Options.AutoLogon) { throw '-AutoLogonUser e -AutoLogonPassword exigem -AutoLogon.' }
}
function Get-PainelCurrentUser { return [Security.Principal.WindowsIdentity]::GetCurrent().Name }
function Get-PainelAclArgs([string]$Launcher, [bool]$ForAllUsers) {
  $argsList = @($Launcher, '/inheritance:r', '/grant:r', "$((Get-PainelCurrentUser)):(M)", '*S-1-5-18:(F)', '*S-1-5-32-544:(F)')
  if ($ForAllUsers) { $argsList += '*S-1-5-32-545:(RX)' }
  return $argsList
}
function New-PainelTaskDefinition([string]$ScreenName, [string]$Launcher, [string]$User, [bool]$ForAllUsers) {
  return @{ Name = "PainelAvisos-Kiosk-$ScreenName"; Launcher = $Launcher; User = $User; AllUsers = $ForAllUsers }
}
function ConvertTo-PainelTaskXml($Definition) {
  $escape = { param($s) [Security.SecurityElement]::Escape([string]$s) }
  $userXml = & $escape $Definition.User
  $triggerUser = if ($Definition.AllUsers) { '' } else { "<UserId>$userXml</UserId>" }
  # GroupId usa o token interativo do membro que fez logon; o schema não permite LogonType com GroupId.
  $principal = if ($Definition.AllUsers) { '<GroupId>S-1-5-32-545</GroupId>' } else { "<UserId>$userXml</UserId><LogonType>InteractiveToken</LogonType>" }
  $arguments = & $escape ('-NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $Definition.Launcher + '"')
  return @"
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled>$triggerUser<Delay>PT20S</Delay></LogonTrigger></Triggers><Principals><Principal id="Kiosk">$principal<RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure></Settings><Actions Context="Kiosk"><Exec><Command>powershell.exe</Command><Arguments>$arguments</Arguments></Exec></Actions></Task>
"@
}
$PainelLsaCs = @'
using System;
using System.Runtime.InteropServices;
public static class PainelLsa {
 [StructLayout(LayoutKind.Sequential)] struct LSA_UNICODE_STRING { public ushort Length; public ushort MaximumLength; public IntPtr Buffer; }
 [StructLayout(LayoutKind.Sequential)] struct Attributes { public uint Length; public IntPtr RootDirectory; public IntPtr ObjectName; public uint Flags; public IntPtr SecurityDescriptor; public IntPtr SecurityQualityOfService; }
 [DllImport("advapi32.dll")] static extern uint LsaOpenPolicy(IntPtr system, ref Attributes attrs, uint access, out IntPtr handle);
 [DllImport("advapi32.dll")] static extern uint LsaStorePrivateData(IntPtr handle, ref LSA_UNICODE_STRING key, IntPtr data);
 [DllImport("advapi32.dll")] static extern uint LsaRetrievePrivateData(IntPtr handle, ref LSA_UNICODE_STRING key, out IntPtr data);
 [DllImport("advapi32.dll")] static extern uint LsaFreeMemory(IntPtr data);
 [DllImport("advapi32.dll")] static extern uint LsaClose(IntPtr handle);
 static LSA_UNICODE_STRING Str(string value) { LSA_UNICODE_STRING s = new LSA_UNICODE_STRING(); s.Buffer = Marshal.StringToHGlobalUni(value); s.Length = checked((ushort)(value.Length * 2)); s.MaximumLength = checked((ushort)(s.Length + 2)); return s; }
 public static bool Secret(string password, bool query) {
  Attributes a = new Attributes(); a.Length = (uint)Marshal.SizeOf(typeof(Attributes)); IntPtr h;
  uint status = LsaOpenPolicy(IntPtr.Zero, ref a, query ? 4u : 32u, out h);
  if (status != 0) throw new InvalidOperationException("Acesso ao LSA indisponível (" + status + ").");
  LSA_UNICODE_STRING key = Str("DefaultPassword"); IntPtr data = IntPtr.Zero; LSA_UNICODE_STRING val = new LSA_UNICODE_STRING();
  try {
   if (query) { status = LsaRetrievePrivateData(h, ref key, out data); if (status == 0) return true; if (status == 0xC0000034u) return false; throw new InvalidOperationException("Consulta LSA indisponível."); }
   if (password != null) { val = Str(password); data = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(LSA_UNICODE_STRING))); Marshal.StructureToPtr(val, data, false); }
   status = LsaStorePrivateData(h, ref key, data); if (password == null && status == 0xC0000034u) return true; if (status != 0) throw new InvalidOperationException("Gravação LSA indisponível (" + status + ")."); return true;
  } finally { if (query && data != IntPtr.Zero) LsaFreeMemory(data); if (!query && data != IntPtr.Zero) Marshal.FreeHGlobal(data); if (val.Buffer != IntPtr.Zero) { for (int i=0;i<val.MaximumLength;i++) Marshal.WriteByte(val.Buffer,i,0); Marshal.FreeHGlobal(val.Buffer); } Marshal.FreeHGlobal(key.Buffer); LsaClose(h); }
 }
}
'@
function Invoke-PainelLsa([string]$Password, [switch]$Delete, [switch]$Query) {
  if (-not ('PainelLsa' -as [type])) { Add-Type -TypeDefinition $PainelLsaCs -Language CSharp }
  if ($Query) { return [PainelLsa]::Secret($null, $true) }
  if ($Delete) { [void][PainelLsa]::Secret($null, $false) } else { [void][PainelLsa]::Secret($Password, $false) }
}
function Get-PainelAutoLogonState {
  $r = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon' -ErrorAction SilentlyContinue
  $secret = 'indisponível (consulta requer permissão LSA)'
  try { $secret = Invoke-PainelLsa -Query } catch {}
  return [pscustomobject]@{ Enabled = ($r.AutoAdminLogon -eq '1'); User = "$($r.DefaultDomainName)\$($r.DefaultUserName)"; PlainPassword = ($null -ne $r.DefaultPassword); LsaSecret = $secret }
}
function Set-PainelAutoLogon([string]$User, [Security.SecureString]$Password) {
  if (-not (Test-PainelAdmin)) { throw 'Logon automático exige administrador. Abra o PowerShell como administrador.' }
  if (-not $User) { $User = Get-PainelCurrentUser }
  if (-not $Password) { $Password = Read-Host "Senha de $User para logon automático" -AsSecureString }
  $parts = $User -split '\\', 2
  $domain = $env:COMPUTERNAME; $name = $User
  if ($parts.Count -eq 2) { $domain = $parts[0]; $name = $parts[1] }
  $path = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
  $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Password)
  try {
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
    try { Invoke-PainelLsa -Password $plain; Remove-ItemProperty $path -Name DefaultPassword -ErrorAction SilentlyContinue }
    catch { Set-ItemProperty $path -Name DefaultPassword -Value $plain; Write-Warning 'LSA indisponível: a senha do logon automático fica em texto claro no registro.' }
    Set-ItemProperty $path -Name DefaultUserName -Value $name
    Set-ItemProperty $path -Name DefaultDomainName -Value $domain
    Set-ItemProperty $path -Name AutoAdminLogon -Value '1'
    Remove-ItemProperty $path -Name AutoLogonCount -ErrorAction SilentlyContinue   # senão o Windows desliga o logon automático após N vezes
  } finally { $plain = $null; [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
}
function Clear-PainelAutoLogon {
  if (-not (Test-PainelAdmin)) { throw 'Desativar logon automático exige PowerShell como administrador.' }
  $path = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
  Set-ItemProperty $path -Name AutoAdminLogon -Value '0'
  Remove-ItemProperty $path -Name DefaultPassword -ErrorAction SilentlyContinue
  Invoke-PainelLsa -Delete
  Write-Host 'Logon automático desativado.'
}
function Show-PainelAutoLogon {
  $state = Get-PainelAutoLogonState
  if ($state.Enabled) { Write-Host "Logon automático ativo: $($state.User)" -ForegroundColor Green }
  else { Write-Host 'ATENÇÃO: sem logon automático o painel só abre depois que alguém entrar no Windows' -ForegroundColor Yellow; Write-Host 'Para ativar (PowerShell como administrador): .\install-tv-kiosk.ps1 -AutoLogon' }
}
function Get-PainelTaskResult([long]$Result) {
  switch ($Result) { 0 { 'sucesso' }; 267011 { 'nunca executou' }; 267009 { 'em execução' }; 267008 { 'pronta' }; default { "erro/código $Result (0x$('{0:X}' -f $Result))" } }
}
function Get-PainelOldLinks([string]$ScreenName) {
  foreach ($folder in 'Startup','CommonStartup') { $dir = [Environment]::GetFolderPath($folder); if ($dir) { Join-Path $dir "Painel de Avisos - $ScreenName.lnk" } }
}
function Stop-PainelScreen([string]$ScreenName) {
  Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.ProcessId -ne $PID -and (Test-PainelLauncherMatch $_.CommandLine $ScreenName) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
  # Edge desta tela de qualquer usuário (com -AllUsers o perfil fica no LOCALAPPDATA de cada um)
  $edgeRx = '--user-data-dir="?[^"]*[\\/]PainelAvisos[\\/]edge-kiosk-' + [regex]::Escape($ScreenName) + '"?(\s|$)'
  Get-CimInstance Win32_Process -Filter "Name='msedge.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -match $edgeRx } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
}
function Remove-PainelStartup([string]$ScreenName) {
  $task = Get-ScheduledTask -TaskName "PainelAvisos-Kiosk-$ScreenName" -ErrorAction SilentlyContinue
  if ($task) { Unregister-ScheduledTask -TaskName "PainelAvisos-Kiosk-$ScreenName" -Confirm:$false -ErrorAction Stop }
  Get-PainelOldLinks $ScreenName | ForEach-Object { if (Test-Path $_) { Remove-Item $_ -Force -ErrorAction Stop } }
}
function Remove-PainelScreen([string]$ScreenName, [string]$Directory) {
  Stop-PainelScreen $ScreenName
  Remove-PainelStartup $ScreenName
  foreach ($file in @((Join-Path $Directory "tv-kiosk-$ScreenName.ps1"), (Join-Path $Directory "tv-kiosk-$ScreenName.log"), (Join-Path $env:LOCALAPPDATA "PainelAvisos\tv-kiosk-$ScreenName.log"))) { if (Test-Path $file) { Remove-Item $file -Force } }
  Write-Host 'Tela removida. O logon automático foi mantido; para desativar use -DisableAutoLogon.'
}
function Show-PainelStatus([string]$ScreenName, [string]$Directory) {
  $taskName = "PainelAvisos-Kiosk-$ScreenName"
  $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
  if ($task) { $info = Get-ScheduledTaskInfo -TaskName $taskName; Write-Host "Tarefa: $taskName; estado: $($task.State); última execução: $($info.LastRunTime); resultado: $($info.LastTaskResult) ($(Get-PainelTaskResult $info.LastTaskResult))" } else { Write-Host "Tarefa ausente: $taskName" }
  Get-PainelOldLinks $ScreenName | ForEach-Object { Write-Host "Atalho antigo: $_ = $(Test-Path $_)" }
  Write-Host "Lançador existe: $(Test-Path (Join-Path $Directory "tv-kiosk-$ScreenName.ps1"))"
  $running = @(Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" -ErrorAction SilentlyContinue | Where-Object { Test-PainelLauncherMatch $_.CommandLine $ScreenName })
  $edgeProfile = Join-Path $env:LOCALAPPDATA "PainelAvisos\edge-kiosk-$ScreenName"
  Write-Host "Lançador rodando: $($running.Count -gt 0); Edge do usuário atual: $(@(Get-PainelKioskEdge $edgeProfile).Count -gt 0)"
  $state = Get-PainelAutoLogonState
  Write-Host "AutoAdminLogon: $($state.Enabled); usuário: $($state.User); DefaultPassword em texto claro: $($state.PlainPassword); segredo LSA: $($state.LsaSecret)"
  $log = Join-Path $env:LOCALAPPDATA "PainelAvisos\tv-kiosk-$ScreenName.log"
  Write-Host "Log do usuário atual: $log"
  if (Test-Path $log) { Get-Content $log -Tail 20 | Write-Host }
}

Assert-PainelOptions $PSBoundParameters
if ($Screen -notmatch '^[a-z0-9][a-z0-9_-]{0,39}$') { throw 'Nome de tela inválido.' }
if ($Status) { Show-PainelStatus $Screen $InstallDir; return }
if ($Uninstall) { Remove-PainelScreen $Screen $InstallDir; return }
if ($DisableAutoLogon) { Clear-PainelAutoLogon; return }
if ($AutoLogon) {
  Set-PainelAutoLogon $AutoLogonUser $AutoLogonPassword
  if (-not ($PSBoundParameters.ContainsKey('Url') -or $PSBoundParameters.ContainsKey('Server') -or $Key)) { Show-PainelAutoLogon; return }
}
if ($AllUsers -and -not (Test-PainelAdmin)) { throw '-AllUsers exige PowerShell como administrador para registrar a tarefa do grupo BUILTIN\Users e ajustar as permissões.' }

if ($ListMonitors) {
  $mons = @(Get-PainelMonitors)
  Write-Host "Monitores (ordem usada por -Monitor: DISPLAY1, DISPLAY2... do Windows):" -ForegroundColor Cyan
  $mons | Select-Object @{ n = "Nº"; e = { $_.Index } }, @{ n = "Principal"; e = { if ($_.Primary) { "sim" } else { "não" } } },
    @{ n = "Dispositivo"; e = { $_.Device -replace '^\\\\\.\\', '' } }, @{ n = "Nome"; e = { $_.Name } },
    @{ n = "Resolução"; e = { "$($_.Width)x$($_.Height)" } }, @{ n = "Posição X,Y"; e = { "$($_.X),$($_.Y)" } }, @{ n = "Escala"; e = { "$($_.Scale)%" } } |
    Format-Table -AutoSize | Out-String | Write-Host
  Write-Host "Use -Monitor <Nº> (ex.: -Monitor 3) ou -Monitor DISPLAYn. O número de Configurações > Tela pode ser diferente: confira pelo número mostrado em cada tela."
  if (-not $NoIdentify -and $mons.Count) { Show-PainelMonitorIds $mons }
  return
}

if ($Screen -notmatch '^[a-z0-9][a-z0-9_-]{0,39}$') { throw "Nome de tela inválido: use minúsculas, números, - ou _ (ex.: tv-principal)" }
if (-not (Test-PainelValidMonitorArg $Monitor)) { throw "-Monitor inválido: use o número da lista de -ListMonitors (ex.: 2) ou DISPLAYn." }
$Monitor = "$Monitor".Trim()
if ($Monitor -match '^(0|principal|primary)$') { $Monitor = "" }
if ($Url) {
  if ($Url -notmatch '^https?://[^/\s]+') { throw "-Url deve ser algo como https://avisos.exemplo.com" }
  $Base = ($Url -replace '/+$', '') -replace '/tela.*$', ''
} else {
  $Base = "http://" + ($Server -replace '^https?://', '' -replace '/+$', '')
}
if ($Key -and $Key -notmatch '^[A-Za-z0-9_-]{8,128}$') { throw "Chave inválida (copie exatamente como o painel mostrou)." }
if (-not $Key) { Write-Warning "Sem -Key: a TV só funcionará se o servidor aceitar telas sem chave (modo LAN legado)." }
if ($Base -like "https://*" -and -not $Key) { throw "Servidor HTTPS exige -Key (gere em Painel > Telas)." }
$TelaUrl = "$Base/tela?screen=$Screen" + $(if ($Key) { "&key=$Key" } else { "" })

$edge = @("${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe", "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe") |
  Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $edge) { throw "Microsoft Edge não encontrado." }

if ($Monitor) {
  try {
    $sel = Select-PainelMonitor (Get-PainelMonitors) $Monitor
    if ($sel.Fallback) { Write-Warning "$($sel.Message). O lançador tenta de novo a cada abertura do Edge." }
    else { Write-Host "==> Quiosque no $($sel.Message)" -ForegroundColor Green }
  } catch { Write-Warning "Não foi possível ler os monitores agora ($($_.Exception.Message)); o lançador tenta na hora de abrir." }
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$launcher = Join-Path $InstallDir "tv-kiosk-$Screen.ps1"
$logFile = Join-Path $env:LOCALAPPDATA "PainelAvisos\tv-kiosk-$Screen.log"
$profileDir = Join-Path $env:LOCALAPPDATA "PainelAvisos\edge-kiosk-$Screen"

@"
# Gerado por install-tv-kiosk.ps1 — abre o Painel de Avisos em tela cheia e mantém aberto.
`$url  = '$TelaUrl'
`$edge = '$edge'
`$monitorWanted = '$Monitor'   # vazio = monitor principal
`$profileDir = Join-Path `$env:LOCALAPPDATA "PainelAvisos\edge-kiosk-$Screen"
`$logFile = Join-Path `$env:LOCALAPPDATA "PainelAvisos\tv-kiosk-$Screen.log"
New-Item -ItemType Directory -Force -Path (Split-Path `$logFile) | Out-Null
`$edgeArgs = @(
  '--kiosk', `$url,
  '--edge-kiosk-type=fullscreen',
  '--no-first-run',
  '--autoplay-policy=no-user-gesture-required',
  "--user-data-dir=``"`$profileDir``"",
  '--disable-features=Translate,msEdgeSidebarV2',
  '--overscroll-history-navigation=0',
  '--disable-pinch'
)
function Write-PainelLog([string]`$msg) {
  try {
    if ((Test-Path `$logFile) -and (Get-Item `$logFile).Length -gt 262144) { Move-Item `$logFile (`$logFile + '.1') -Force }
    Add-Content -Path `$logFile -Value ((Get-Date -Format 'yyyy-MM-dd HH:mm:ss') + '  ' + `$msg) -Encoding UTF8
  } catch {}
}
# Um único vigia por tela, antes da espera pelo servidor.
`$mutex = New-Object System.Threading.Mutex(`$false, 'Local\PainelAvisos-Kiosk-$Screen')
try { `$acquired = `$mutex.WaitOne(0) } catch [System.Threading.AbandonedMutexException] { `$acquired = `$true }
if (-not `$acquired) { Write-PainelLog 'já existe outro lançador desta tela; saindo'; exit }
Write-PainelLog 'lançador iniciado'
Write-PainelLog 'aguardando o servidor responder...'
`$deadline = (Get-Date).AddSeconds($WaitSeconds)
`$nextWaitLog = (Get-Date).AddSeconds(30)
`$responded = `$false
while ((Get-Date) -lt `$deadline) {
  try { if ((Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Uri '$Base/healthz').StatusCode -eq 200) { `$responded = `$true; break } } catch {}
  if ((Get-Date) -ge `$nextWaitLog) { Write-PainelLog 'aguardando o servidor responder...'; `$nextWaitLog = (Get-Date).AddSeconds(30) }
  Start-Sleep -Seconds 5
}
if (`$responded) { Write-PainelLog 'servidor respondeu' } else { Write-PainelLog 'tempo de espera esgotado; abrindo com cache offline se disponível' }

$MonitorLib

# Edge deste quiosque = processos msedge com ESTE perfil na linha de comando.
# Não usamos WaitForExit: o msedge.exe lançado pode sair na hora (entrega a URL a um Edge já aberto),
# e reabrir em loop abriria a tela de novo a cada poucos segundos (o rodízio nunca chegaria ao 2º aviso).
function Test-KioskEdge { (Get-PainelKioskEdge `$profileDir).Count -gt 0 }

# monitor escolhido, resolvido na hora (resolução/ordem podem ter mudado); `$null = principal sem ajuste
function Resolve-KioskMonitor([bool]`$Quiet) {
  if (-not `$monitorWanted) { return `$null }
  try {
    `$sel = Select-PainelMonitor (Get-PainelMonitors) `$monitorWanted
    if (-not `$Quiet -or `$sel.Fallback) { Write-PainelLog ("monitor: " + `$sel.Message + " [DPI: " + [PainelMon]::DpiMode + "]") }
    return `$sel
  } catch { Write-PainelLog ("monitor: falha ao ler monitores (" + `$_.Exception.Message + "); abrindo no principal"); return `$null }
}

`$lastCheck = Get-Date
`$lastFallback = `$false
# abre o Edge e reabre só se ele tiver sido fechado
while (`$true) {
  if (-not (Test-KioskEdge)) {
    `$sel = Resolve-KioskMonitor `$false
    `$extra = @()
    if (`$sel -and `$sel.Monitor) { `$extra = Get-PainelEdgePlacementArgs `$sel.Monitor }
    Start-Process -FilePath `$edge -ArgumentList (`$edgeArgs + `$extra) | Out-Null
    Write-PainelLog "Edge aberto"
    if (`$sel -and `$sel.Monitor) {
      `$res = Set-PainelKioskPlacement `$profileDir `$sel.Monitor
      Write-PainelLog ("posição: " + `$res.Message)
      `$lastFallback = `$sel.Fallback
    } else { Start-Sleep -Seconds 15 }
    `$lastCheck = Get-Date
  } elseif (`$monitorWanted -and ((Get-Date) - `$lastCheck).TotalSeconds -ge 30) {
    # a cada 30 s: se a janela saiu do monitor (ex.: monitor desligado e religado), devolve
    `$lastCheck = Get-Date
    `$sel = Resolve-KioskMonitor `$true
    if (`$sel -and `$sel.Monitor -and (-not `$sel.Fallback -or -not `$lastFallback)) {
      `$res = Set-PainelKioskPlacement `$profileDir `$sel.Monitor 3 0
      if (`$res.Moved -or -not `$res.Ok) { Write-PainelLog ("posição (verificação): " + `$res.Message) }
    }
    if (`$sel) { `$lastFallback = `$sel.Fallback }
  }
  Start-Sleep -Seconds 5
}
"@ | Set-Content -Path $launcher -Encoding UTF8
# A chave fica no lançador; -AllUsers concede leitura aos usuários locais.
$aclArgs = Get-PainelAclArgs $launcher ([bool]$AllUsers)
& icacls @aclArgs | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'Falha ao aplicar permissões ao lançador (icacls).' }
Stop-PainelScreen $Screen
Remove-PainelStartup $Screen
$definition = New-PainelTaskDefinition $Screen $launcher (Get-PainelCurrentUser) ([bool]$AllUsers)
try { Register-ScheduledTask -TaskName $definition.Name -Xml (ConvertTo-PainelTaskXml $definition) -Force -ErrorAction Stop | Out-Null }
catch { throw "Falha ao registrar a tarefa $($definition.Name). Verifique as permissões (para -AllUsers, execute como administrador). $($_.Exception.Message)" }
Write-Host "==> Tarefa agendada criada: $($definition.Name)" -ForegroundColor Green
Write-Host "Log do usuário atual: $logFile"
if ($AllUsers) { Write-Warning 'Com -AllUsers, a chave no lançador fica legível por usuários locais.' }

if (-not $SkipPowerSettings) {
  powercfg /change monitor-timeout-ac 0 | Out-Null
  powercfg /change standby-timeout-ac 0 | Out-Null
  powercfg /change hibernate-timeout-ac 0 | Out-Null
  Set-ItemProperty -Path "HKCU:\Control Panel\Desktop" -Name ScreenSaveActive -Value "0"
  Set-ItemProperty -Path "HKCU:\Control Panel\Desktop" -Name ScreenSaveTimeOut -Value "0" -ErrorAction SilentlyContinue
  Write-Host "==> Tela/suspensão/proteção de tela desativadas (usuário atual)"
}
Write-Host ""
Write-Host "Recomendações:" -ForegroundColor Cyan
Show-PainelAutoLogon
Write-Host " - Na BIOS, ative 'Restore on AC Power Loss = Power On'."
Write-Host " - Para sair do quiosque: Alt+F4 (o Edge é reaberto em até 5 s; para parar, finalize o 'powershell' desta tela no Gerenciador de Tarefas)."

if (-not $NoStart) { Start-ScheduledTask -TaskName $definition.Name -ErrorAction Stop }
