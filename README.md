<div align="center">

# 🤖 Secretária de IA no WhatsApp

**Uma assistente pessoal que vive no WhatsApp: agenda, lembretes, notas, lista de compras — e um gestor financeiro para o casal que entende texto, áudio e foto de comprovante.**

[![CI](https://github.com/ryujiee/AI-assistent/actions/workflows/ci.yml/badge.svg)](https://github.com/ryujiee/AI-assistent/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vue.js&logoColor=white)
![TypeScript](https://img.shields.io/badge/TypeScript-5-3178c6?logo=typescript&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15-4169e1?logo=postgresql&logoColor=white)
![OpenAI](https://img.shields.io/badge/OpenAI-function%20calling-412991?logo=openai&logoColor=white)

<img src="docs/screenshots/overview.png" alt="Painel financeiro com gastos do mês, evolução e destaques" width="100%">

</div>

---

## ✨ O que ela faz

### 🗓️ Secretária pessoal (conversa privada)
Você manda mensagem — texto, **áudio** ou **foto** — e ela executa ações reais via *function calling*:

| Você escreve | Ela faz |
|---|---|
| *"Agende dentista amanhã às 14h"* | cria o compromisso e avisa **15 min antes** |
| *"Me lembra em 20 minutos de tirar o bolo"* | timer que sobrevive a reinícios |
| *"Anota que a senha do Wi-Fi é 1234"* / *"qual a senha do Wi-Fi?"* | bloco de notas com busca |
| *"Coloca leite, ovos e café na lista"* | lista de compras sem duplicados |
| — | **resumo matinal** dos compromissos do dia |

### 💰 Gestor financeiro (grupo do casal)
Um grupo de WhatsApp com vocês dois e a Secretária vira a interface das finanças:

<table>
<tr>
<td width="36%"><img src="docs/screenshots/whatsapp-chat.png" alt="Conversa no grupo: gastos registrados, pergunta de categoria e relatório do mês"></td>
<td>

- **Registro em linguagem natural** — *"gastei 42 no almoço"*, *"paguei a fatura do cartão 1.200"*, *"95 no ifood ontem"*.
- **Áudio e comprovante** — transcrição (Whisper) e leitura de PIX/recibo com extração estruturada.
- **Pergunta só o que falta** — sem categoria? *"Foi com o quê?"*. A resposta é resolvida pelo backend, de forma determinística, não "adivinhada" pelo modelo.
- **Correções e desfazer** — *"na verdade foi 60"*, *"apaga esse"*, respondendo (citando) a mensagem certa.
- **Relatórios na conversa** — *"quanto gastamos esse mês?"*, por pessoa e categoria, comparando com o mesmo período do mês anterior.
- **Alertas de orçamento** em 70 %, 80 % e 100 %, e resumos semanais/mensais.
- **Painel web** com dashboard, lançamentos, categorias e orçamentos.

</td>
</tr>
</table>

## 🖥️ Painel

| Lançamentos | Categorias & orçamentos |
|---|---|
| <img src="docs/screenshots/transactions.png" alt="Lista de lançamentos com filtros"> | <img src="docs/screenshots/categories.png" alt="Categorias com orçamento mensal e consumo"> |
| **WhatsApp financeiro** | **Secretária** |
| <img src="docs/screenshots/finance-whatsapp.png" alt="Grupo vinculado, membros e preferências"> | <img src="docs/screenshots/secretary.png" alt="Conexão do WhatsApp, número alvo e eventos"> |

## 🏗️ Arquitetura

```mermaid
flowchart LR
    WA([WhatsApp]) <--> WM[whatsmeow<br/>sessão no Postgres]
    WM --> R{Roteador}
    R -->|conversa privada<br/>com o dono| SEC[Secretária<br/>OpenAI + tools]
    R -->|grupo vinculado| INB[(Inbox<br/>idempotente)]
    INB --> AG[Agente financeiro<br/>regras + OpenAI + tools estritas]
    AG --> LED[Ledger auditado<br/>centavos int64]
    SEC --> DB[(PostgreSQL)]
    LED --> DB
    CRON[Agendador<br/>resumos · lembretes · timers] --> WM
    UI[Painel Vue 3] <-->|API HTTP + sessão| API[Go net/http]
    API --> DB
```

**Decisões que valem destacar**

- **Determinístico antes do modelo.** Confirmações (*"sim"*, *"não"*), correções de valor e respostas de categoria são resolvidas por código contra o estado salvo da conversa; o modelo só entra quando precisa interpretar.
- **Tools estritas e escopadas.** Esquemas com `DisallowUnknownFields`, workspace injetado pelo servidor, lista de transações permitidas por turno, no máximo 5 alterações por mensagem e checagem de que o valor novo aparece na mensagem. Comprovantes de terceiros só podem *criar* lançamentos (defesa contra *prompt injection*).
- **Dinheiro é inteiro.** Valores em centavos (`int64`) de ponta a ponta; datas como `DATE`; fuso **America/Sao_Paulo** concentrado no pacote `timeutil`.
- **Ledger auditado e idempotente.** Toda alteração é uma transação com evento *antes/depois*; cada mensagem do WhatsApp é processada uma única vez, mesmo se reentregue.
- **Comportamento humano no WhatsApp.** Envios com horário aleatório e *jitter* para não parecer automação (padrão que leva a banimento).
- **Segurança do painel.** Senha comparada em tempo constante, cookie assinado (`HttpOnly`, `Secure`, `SameSite=Strict`), checagem de `Origin` em todo POST, *rate limit* de login, CSP, limites de corpo e timeouts no servidor. Banco e painel só em `127.0.0.1`, atrás de proxy reverso.

## 🧰 Stack

| Camada | Tecnologias |
|---|---|
| Backend | Go 1.25 · `net/http` · `pgx` · `robfig/cron` · `whatsmeow` · `go-openai` |
| IA | GPT-4o com *function calling* · saída estruturada · Whisper |
| Banco | PostgreSQL 15 · migrations versionadas com checksum e *advisory lock* |
| Painel | Vue 3 · Vite · TypeScript · Tailwind CSS · Vitest |
| Infra | Docker (multi-stage, usuário sem privilégios) · Nginx · GitHub Actions |

## 🚀 Rodando localmente

Pré-requisitos: Go 1.25+, Node 24+, Docker.

```bash
# 1. Banco (só em localhost:5435)
docker compose up -d postgres

# 2. Backend
cd backend
export DATABASE_URL="postgres://secretary_user:secretary_password@localhost:5435/secretary_db?sslmode=disable"
export ADMIN_PASSWORD="uma-senha-longa-e-unica"            # login do painel (12+ caracteres)
export SESSION_SECRET="$(openssl rand -hex 32)"
export OPENAI_API_KEY="sk-..."
go run . migrate apply      # migrations são aplicadas manualmente
go run .                    # API + painel em http://localhost:8000

# 3. Painel em modo dev (opcional, com hot reload em :5173)
cd ../frontend && npm ci && npm run dev
```

### Modo demonstração (sem WhatsApp e sem OpenAI)

Dá para explorar tudo sem conta real: um gateway falso de WhatsApp e um "modelo" por regras (use o mesmo banco e as migrations do passo anterior).

```bash
cd backend
FINANCE_ENABLED=true WHATSAPP_FAKE=connected OPENAI_FAKE=true \
ADMIN_PASSWORD=local-dev-password-123 go run .

# em outro terminal: popula um casal fictício (Ana & Bruno) pelo mesmo caminho do WhatsApp
cd frontend && BASE_URL=http://localhost:8000 ADMIN_PASSWORD=local-dev-password-123 node scripts/demo-seed.mjs
```

As imagens deste README foram geradas exatamente assim (`node scripts/screenshots.mjs`).

## 🧪 Testes

```bash
# backend — os testes de banco usam um schema descartável num banco *_test
export TEST_DATABASE_URL="postgres://secretary_user:secretary_password@localhost:5435/secretary_test?sslmode=disable"
cd backend && go vet ./... && go test -race ./...

# painel
cd frontend && npm run typecheck && npm test
```

O CI roda tudo isso a cada push e PR, com um Postgres real.

## 🌐 Produção

O deploy é uma imagem Docker (o painel vai embutido) atrás de um Nginx com HTTPS:

```bash
cp .env.example .env    # OPENAI_API_KEY, ADMIN_PASSWORD, SESSION_SECRET, POSTGRES_PASSWORD, FINANCE_ENABLED
docker compose up -d --build
docker exec secretary_backend ./secretary migrate apply
```

O script `deploy.sh` faz *push*, atualiza o servidor, reconstrói só o backend e confere a saúde (`SECRETARY_SERVER` e `SECRETARY_HEALTH_URL` definem o destino). O Postgres nunca é recriado, então a agenda e a sessão do WhatsApp são preservadas.

## 📚 Documentação

- [Arquitetura do gestor financeiro](docs/finance/ARCHITECTURE.md)
- [Setup e variáveis de ambiente](docs/finance/SETUP.md)
- [Uso pelo WhatsApp](docs/finance/WHATSAPP.md)
- [Segurança e privacidade](docs/finance/SECURITY.md)

## 📁 Estrutura

```
backend/
  main.go          wiring: WhatsApp, IA, agendador, finanças e API
  whatsapp/        conexão whatsmeow, roteamento, gateway real e falso
  openai/          loop de tools, saída estruturada, transcrição
  engine/          cron, resumo matinal, lembretes e timers
  finance/         inbox, agente, ledger, relatórios, orçamentos, comprovantes
  web/             API HTTP, autenticação, painel estático
  db/              pool, migrations embutidas, helpers da secretária
  timeutil/        fuso America/Sao_Paulo
frontend/
  src/app/         shell, login, rotas
  src/features/    secretary/ e finance/ (dashboard, lançamentos, categorias, WhatsApp)
  scripts/         seed de demonstração, capturas e QA visual
docs/              documentação, capturas e histórico de implementação
```
