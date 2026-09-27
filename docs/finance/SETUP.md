# Gestor Financeiro — Setup

## Variáveis de ambiente

| Variável | Obrigatória | Padrão | Uso |
|---|---|---|---|
| `ADMIN_PASSWORD` | sim (painel) | — | Senha do painel, mínimo 12 caracteres. Sem ela o painel fica bloqueado (a Secretária continua no WhatsApp). |
| `SESSION_SECRET` | recomendada | aleatório no boot | Assina o cookie de sessão (32+ caracteres). Sem ela, todo restart desloga. |
| `FINANCE_ENABLED` | não | `false` | Liga o módulo financeiro. **Não cria tabelas.** |
| `OPENAI_API_KEY` | sim (IA) | — | Agente financeiro, comprovantes e Whisper. |
| `WHATSAPP_LOG_LEVEL` | não | `INFO` | Nível de log do whatsmeow (`DEBUG` despeja protocolo). |
| `CORS_ALLOWED_ORIGINS` | não | vazio | Origens extras com credenciais (o painel é same-origin; em produção fica vazio). |
| `WHATSAPP_FAKE` | só dev | — | `connected` ou `qr`: não abre conexão com o WhatsApp; grupos fictícios e rotas `/api/dev`. **Nunca em produção.** |
| `OPENAI_FAKE` | só dev | — | Modelo por regras no lugar da OpenAI (sem chave). **Nunca em produção.** |
| `DATABASE_URL`, `PORT` | — | como antes | Inalteradas. |
| `TEST_DATABASE_URL` | testes | — | Banco `*_test` para os testes de SQL. |

## Migrations

Versionadas em `backend/db/migrations/NNN_nome.sql`, embutidas no binário, registradas em `schema_migrations` com checksum.

```bash
cd backend
go run . migrate status
go run . migrate apply          # todas as pendentes
go run . migrate apply 002      # até a 002
```

- Só a `001_baseline` (as tabelas que a Secretária sempre criou no boot, todas `IF NOT EXISTS`) roda sozinha no boot.
- `002_finance` e futuras **só** com `migrate apply`. Um deploy nunca muda o schema sozinho.
- Editar uma migration já aplicada bloqueia o `apply` (checksum).

## Desenvolvimento local

```bash
docker compose up -d postgres                     # porta 5435
docker exec secretary_postgres psql -U secretary_user -d secretary_db -c "CREATE DATABASE secretary_test"

export DATABASE_URL="postgres://secretary_user:secretary_password@localhost:5435/secretary_db?sslmode=disable"
export TEST_DATABASE_URL="postgres://secretary_user:secretary_password@localhost:5435/secretary_test?sslmode=disable"
export ADMIN_PASSWORD="local-dev-password-123"
export SESSION_SECRET="local-dev-session-secret-0123456789abcdef"
export FINANCE_ENABLED=true WHATSAPP_FAKE=connected OPENAI_FAKE=true

cd backend && go run . migrate apply && go run .   # :8000
cd frontend && npm ci && npm run dev               # :5173 (proxy /api -> :8000) ou npm run build
```

Dados fictícios (Ana e Bruno) passando pelo pipeline real:

```bash
cd frontend && BASE_URL=http://localhost:8000 ADMIN_PASSWORD=local-dev-password-123 node scripts/demo-seed.mjs
```

Injetar uma mensagem no grupo fictício:

```bash
curl -b cookie -H 'Origin: http://localhost:8000' -H 'Content-Type: application/json' \
  -d '{"chat_jid":"120363000000000001@g.us","sender_jid":"5511900000001@s.whatsapp.net","push_name":"Ana","text":"gastei 50 no mercado"}' \
  http://localhost:8000/api/dev/whatsapp/messages
curl -b cookie http://localhost:8000/api/dev/whatsapp/outbox
```

## Testes

```bash
cd backend && go vet ./... && go test ./...          # SQL tests usam TEST_DATABASE_URL (pulados sem ela)
cd frontend && npm run typecheck && npm test && npm run build
cd frontend && node scripts/e2e.mjs                  # E2E local (banco novo, fakes ligados)
cd frontend && node scripts/ui-qa.mjs                # overflow, texto cortado, rótulos sobrepostos, erros
cd frontend && node scripts/screenshots.mjs [rotas]  # Chrome do sistema via playwright-core
```

## Produção (passos para quando você decidir — nada disso foi executado)

1. No `.env` do servidor: `ADMIN_PASSWORD`, `SESSION_SECRET`. Deploy (`./deploy.sh`). O painel passa a exigir login; `deploy.sh` agora checa `/api/health`.
2. Backup do banco.
3. `docker exec secretary_backend ./secretary migrate status` e depois `... migrate apply` (cria as tabelas `finance_*`).
4. `FINANCE_ENABLED=true` no `.env` e `docker compose up -d backend`. Com a migration pendente o módulo espera e loga um aviso; a Secretária não é afetada.
5. Criar no WhatsApp um grupo com vocês dois e o número da Secretária; no painel, Financeiro → WhatsApp Financeiro → Escolher grupo.
6. Opcional: resumo mensal/semanal e orçamentos no painel.

Rollback: `FINANCE_ENABLED=false` desliga o módulo (as mensagens do grupo passam a ser ignoradas). As tabelas podem ficar; nada da Secretária depende delas.
