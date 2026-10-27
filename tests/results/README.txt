Resultados dos testes de integração (Docker, Traefik real, backup/restore), executados em 20/10/2026 no ambiente de build.
Chaves e tokens foram mascarados ([REDACTED]).
- traefik_result.txt ........... Traefik v3.5.6: 9/9
- traefik_v2_result.txt ........ Traefik v2.11: 9/9
- traefik_compress_result.txt .. NEGATIVO esperado: middleware compress SEM exclusão quebra o SSE
- traefik_compress_excluded.txt  compress com excludedcontenttypes=text/event-stream: 9/9
- allowlist_*.txt .............. ipAllowList v3 (bloqueio/liberação) e ipWhiteList v2
- backup_result.txt / restore_result.txt  backup.sh e restore.sh
- allowlist_block_full_v2.txt .. matriz completa de bloqueio (v2 ipWhiteList)
- upgrade_1.1.0_to_1.1.1.txt .. atualização com os comandos do README: dados, chaves e senha preservados no volume
- upgrade_1.1.3_to_1.2.0.txt .. atualização real 1.1.3 → 1.2.0 na mesma pasta de dados: configurações e avisos preservados, padrões novos do urgente aplicados
