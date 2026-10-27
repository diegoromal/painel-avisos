# Histórico de alterações

Todas as alterações relevantes são registradas aqui, em formato simples inspirado no Keep a Changelog.

## [1.3.0] - 2026-10-27

### Alterado

- Projeto independente e público, sem referências a clientes específicos.
- README reescrito para instalação e uso genéricos.
- Histórico de versões separado neste CHANGELOG.
- `.gitignore` adicionado e `dist/` removido do repositório; os binários continuam sendo gerados por `build.sh`.

### Previsto (implementação em etapa seguinte)

- Botão "Instalar na TV" no painel administrativo, em Telas.
- Endpoint `/install/tv-kiosk.ps1` para obter o instalador da TV.

## [1.2.1]

- Instalador Windows: tarefa agendada por tela, logon automático opcional, diagnóstico e desinstalação.
- Só o script do Windows mudou funcionalmente; o servidor não precisa ser atualizado.

## [1.2.0]

- **Aviso urgente intercalado (novo padrão):** o urgente fica 60 s na TV, com alerta sonoro a cada 10 s. Depois passa um ciclo dos avisos normais e o urgente volta, apitando de novo. Antes ele ocupava a tela sozinho e parava o rodízio.
- Novas opções em **Configurações > Avisos urgentes**: modo (*Intercalar com os avisos normais* ou *Fixo na tela até sair do ar*), tempo do urgente na tela (10 a 600 s, padrão 60) e repetir o alerta a cada 10 s (padrão ligado).
- Instalações existentes passam para o novo padrão sozinhas ao atualizar, sem perder nada. Para manter o comportamento antigo, escolha *Fixo na tela até sair do ar*.
- A TV agora recarrega sozinha quando o servidor é atualizado, mantendo a posição do rodízio. Isso vale a partir desta versão.

## [1.1.3]

- Quiosque em **PC com vários monitores**: `install-tv-kiosk.ps1 -ListMonitors` e `-Monitor <n>`.
- Várias telas no mesmo PC: reinstalar uma tela não fecha mais o Edge de outra quando um nome é começo do outro (ex.: `tv` e `tv-2`).
- Só mudou o script do Windows. O servidor não mudou: atualizar o servidor é opcional (só muda a versão exibida).

## [1.1.2]

- **Correção:** com 2 ou mais avisos normais, a TV podia ficar presa no primeiro aviso. A barra do rodízio enchia, mas a troca não acontecia. A causa era a página da TV ser reaberta a cada poucos segundos, o que voltava sempre ao 1º aviso.
  - O lançador do quiosque (`install-tv-kiosk.ps1`) esperava o `msedge.exe` terminar e reabria o Edge 5 s depois. Quando já havia um Edge aberto com o mesmo perfil (por exemplo, o instalador executado 2 vezes, ou dois atalhos de inicialização), o `msedge.exe` saía na hora e abria a tela de novo em outra aba, sem parar.
  - Agora existe um único lançador por tela. Ele só reabre o Edge se não houver nenhum Edge do quiosque rodando. Reinstalar encerra os lançadores antigos e remove o atalho duplicado.
  - A TV também guarda a posição do rodízio: se a página for reaberta, ela continua do aviso em que estava. O tempo é medido com relógio monotônico, então um ajuste do relógio do Windows não trava a troca.
- Cache da TV atualizado (`painel-tela-v4`). A TV pega o código novo na próxima vez que a página abrir.
- **Na TV:** execute de novo o `install-tv-kiosk.ps1` (mesmos parâmetros). Isso encerra o loop antigo e reabre o Edge já com a versão nova.

## [1.1.1]

- A tela **Configurações** não mostra mais informações técnicas do servidor (modo, proxies, IP). Elas continuam no log de inicialização e, se quiser, numa seção recolhida **"Detalhes técnicos"** só para admin (`SHOW_SERVER_INFO=1`).
- Textos do painel e da TV revisados para o cliente final: "Integrações" no lugar de "API/Tokens", "Aviso automático" no lugar de "Webhook", mensagens de erro sem códigos técnicos, versão discreta no rodapé.
- Nenhuma mudança no comportamento de segurança.

## [1.1.0]

| Recurso | Resumo |
|---|---|
| Chave por tela | Cada TV tem uma chave aleatória (só o hash fica no banco). Ela é exibida uma vez e pode ser regenerada ou revogada; a TV é derrubada na hora. Sem chave válida, a TV mostra **"Tela não autorizada"** (HTTP 401/403). |
| Uploads protegidos | `/uploads/` exige sessão logada ou chave de tela válida. |
| Proxy reverso | `TRUST_PROXY` diz em quais proxies confiar para `X-Forwarded-For/Proto`, usados no IP real (limite de login, auditoria), no cookie `Secure` e no HSTS. |
| Cabeçalhos | CSP estrita (sem script/style inline), HSTS em HTTPS, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, `nosniff`, COOP/CORP. |
| Webhook anti-SSRF | Bloqueia loopback, redes privadas, link-local, CGNAT, metadados de nuvem (169.254.169.254, fd00:ec2::254), NAT64 e equivalentes IPv6. A checagem acontece **no momento da conexão** (derrota DNS rebinding) e também nos redirecionamentos. |
| 2FA (TOTP) | Opcional por usuário, com QR code. O código é pedido no login, com anti-replay. Se o celular se perder, um admin reseta. |
| Offline | Service worker guarda a página, os arquivos e as imagens da TV. Se a chave for revogada, o cache é apagado. |

