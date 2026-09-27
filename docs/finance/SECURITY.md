# Gestor Financeiro — Segurança e privacidade

## Situação anterior

O painel era público e sem autenticação em `secretaria.infinitytech.net.br`: com o WhatsApp desconectado, qualquer pessoa via o QR de pareamento (e podia vincular o próprio celular à conta do bot) e qualquer pessoa trocava o número alvo. CORS aceitava `*`. Isso foi corrigido antes de qualquer dado financeiro existir (PR 1).

## Autenticação

- Senha única em `ADMIN_PASSWORD` (mínimo 12 caracteres). Ausente ou curta: o painel fica bloqueado (login responde 503) e a Secretária continua funcionando no WhatsApp — nunca um modo sem autenticação.
- Comparação em tempo constante (SHA-256 dos dois lados + `subtle.ConstantTimeCompare`). A senha não é guardada em banco nem no cookie.
- Cookie `secretary_session`: `v1|id aleatório|expiração` + HMAC-SHA256 com `SESSION_SECRET`. `HttpOnly`, `Secure`, `SameSite=Strict`, 7 dias. Logout revoga o id em memória até a expiração.
- Rate limit do login: 5 falhas por IP em 15 min bloqueiam o IP; 30 falhas globais em 15 min bloqueiam todo login por 5 min (o IP atrás de proxy nem sempre é confiável). `X-Real-IP` só é aceito quando o peer direto é loopback/privado.
- Toda rota `/api/*` exige sessão, exceto `/api/health`, `/api/session`, `/api/login` e `/api/logout`.

## CSRF, CORS, cabeçalhos

- Requisições que alteram estado exigem `Origin` (ou `Referer`) do mesmo host ou de `CORS_ALLOWED_ORIGINS`; sem ambos, 403. Soma-se ao `SameSite=Strict`.
- CORS só para origens listadas (vazio em produção; o painel é same-origin). Nunca `*`.
- `Content-Security-Policy` no painel (scripts só do próprio domínio), `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy: same-origin`.
- Erros para o cliente são mensagens amigáveis com código estável; detalhes internos só no log.

## Isolamento (ownership)

- Todo dado financeiro tem `workspace_id`; toda consulta filtra por ele.
- O workspace vem do servidor: da sessão (painel) ou do grupo vinculado (WhatsApp). Nunca do corpo da requisição nem do modelo.
- Corpos JSON rejeitam campos desconhecidos (um `workspace_id` enviado pelo cliente gera 400).
- Referências (categoria, pagador, comprovante, duplicata) são verificadas contra o workspace antes de gravar.
- Testes cobrem leitura, edição, exclusão, categorias e comprovantes de outro workspace (404/validação).

## IA e prompt injection

- Mensagens e comprovantes são dados não confiáveis.
- A extração de comprovantes é uma chamada separada **sem ferramentas**, com schema estrito; o agente recebe só os campos, rotulados como não confiáveis.
- Ferramentas estritas, sem parâmetro de workspace/conta; argumentos desconhecidos rejeitados.
- Travas no código, independentes do prompt: alterar/apagar só transações do contexto da conversa; no máximo 5 alterações por mensagem; undo só da última ação de quem escreveu.
- O modelo nunca calcula totais: relatórios vêm prontos do backend.
- Para a OpenAI vai só o necessário: mensagem atual, 8 mensagens recentes do grupo, até 5 transações recentes, nomes de categorias e membros; o comprovante (necessário para a leitura).

## Dados sensíveis

- CPF, CNPJ, chaves PIX aleatórias, e-mails, telefones, agência e conta são removidos do texto livre antes de gravar (mensagem, descrição, estabelecimento, observações).
- O schema de extração não tem campos para CPF, chave, agência ou conta; `external_ref` só guarda um ID E2E de PIX bem formado.
- Comprovantes: BYTEA no Postgres, sem URL pública; servidos só por `GET /api/finance/attachments/{id}` autenticado, do mesmo workspace, com `Cache-Control: no-store`, `nosniff`, nome de arquivo fixo e CSP restritiva para imagens. Tipo validado pelos bytes; até 10 MB.
- A referência de mídia do WhatsApp (chave de download) é apagada do inbox quando a mensagem termina (DONE/FAILED).

## Logs

- JSON via `slog`. Eventos financeiros levam ação, workspace, membro (ids internos), transações, intenção, confiança, duração, tentativa e resultado.
- Nunca: texto de mensagem, valores, comprovantes, telefones/JIDs, senha, cookies, tokens. IP de login aparece só como hash.
- whatsmeow em `INFO` por padrão (antes `DEBUG`).
- Logs da Secretária deixaram de imprimir texto de mensagens, transcrições, títulos de compromissos e motivos de timers.
- Teste automatizado verifica que um turno com valor e CPF não deixa esses dados no log nem no inbox.

## Recomendações de infraestrutura (não aplicadas — decisão sua)

1. Publicar o backend só em `127.0.0.1:8000` no `docker-compose.yml` (`"127.0.0.1:8000:8000"`); hoje a porta 8000 também fica exposta fora do Nginx, o que torna o IP visto pelo rate limit menos confiável.
2. Definir `SESSION_SECRET` fixo no `.env`.
3. HSTS no Nginx (`add_header Strict-Transport-Security "max-age=31536000" always;`).
4. Backup do volume do Postgres (comprovantes agora ficam nele).
5. Trocar as credenciais padrão do Postgres/pgAdmin do compose e não expor o pgAdmin publicamente.
