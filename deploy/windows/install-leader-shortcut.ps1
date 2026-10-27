<#
.SYNOPSIS  Cria na Área de Trabalho do líder um atalho "Publicar aviso" para o painel.
.EXAMPLE   .\install-leader-shortcut.ps1 -Server 192.168.1.195:8080
#>
param([Parameter(Mandatory = $true)][string]$Server)
$Server = $Server -replace '^https?://', '' -replace '/+$', ''
$path = Join-Path ([Environment]::GetFolderPath("Desktop")) "Publicar aviso.url"
"[InternetShortcut]`r`nURL=http://$Server/admin`r`n" | Set-Content -Path $path -Encoding ASCII
Write-Host "Atalho criado: $path -> http://$Server/admin" -ForegroundColor Green
