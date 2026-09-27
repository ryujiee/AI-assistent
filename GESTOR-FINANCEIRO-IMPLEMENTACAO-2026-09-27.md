# Gestor Financeiro por IA: relatório de implementação

**Data:** 27/09/2026.
**Situação:** implementado em 9 PRs empilhados (#1 a #9), sem merge e sem deploy.

**Não foi feito, por estar fora da autorização:**
- nenhuma mudança em produção;
- nenhuma conexão com WhatsApp real;
- nenhum uso de dados reais;
- nenhuma chamada à OpenAI real (não havia chave local).

Toda a validação usou Postgres local, o gateway fake do WhatsApp e o modelo fake por regras.

---

## 1. Arquitetura final

- Um único backend Go: a mesma sessão whatsmeow, o mesmo Postgres, o mesmo cron e o mesmo cliente OpenAI da Secretária.
- O módulo `finance` concentra o domínio.
- O painel virou uma SPA Vite + Vue 3 + TypeScript servida pelo próprio Go.
- Princípio: a IA interpreta e o backend valida, decide e grava.

Detalhes em [docs/finance/ARCHITECTURE.md](docs/finance/ARCHITECTURE.md).

```
WhatsApp (whatsmeow) ─► ExtractMessage ─► Decide ─┬─► Secretária (privado do número alvo; fluxo original)
                                                   ├─► Ingestor.Accept ─► finance_inbox (idempotente)
                                                   │        └─► worker ordenado por workspace ─► Agent
                                                   │              ├─ áudio: Whisper
                                                   │              ├─ comprovante: extração SEM tools
                                                   │              └─ RunTools (tools estritas) ─► ledger / relatórios
                                                   │                     └─► resposta citando a mensagem + alertas
                                                   └─► ignorado (outros chats; nada é baixado)
Painel (Vite/Vue) ─► /api/* (sessão, Origin, workspace do servidor) ─► mesmo ledger e relatórios
Cron existente ─► resumos mensal/semanal
```

## 2. PRs

| PR | Branch | Conteúdo |
|---|---|---|
| [#1](https://github.com/ryujiee/AI-assistent/pull/1) | `feat/finance-01-foundation` | Autenticação do painel, frontend Vite, migration runner, fake do WhatsApp |
| [#2](https://github.com/ryujiee/AI-assistent/pull/2) | `feat/finance-02-ledger` | Schema financeiro, ledger + auditoria, categorias, parser de valores e datas, API de CRUD |
| [#3](https://github.com/ryujiee/AI-assistent/pull/3) | `feat/finance-03-whatsapp` | Router por chat, grupo + membros, inbox idempotente, tela WhatsApp Financeiro |
| [#4](https://github.com/ryujiee/AI-assistent/pull/4) | `feat/finance-04-agent` | `RunTools` extraído, agente financeiro (texto/áudio), correções e undo |
| [#5](https://github.com/ryujiee/AI-assistent/pull/5) | `feat/finance-05-receipts` | Comprovantes (imagem/PDF), armazenamento privado, duplicidade |
| [#6](https://github.com/ryujiee/AI-assistent/pull/6) | `feat/finance-06-queries` | Relatórios determinísticos e consultas pelo WhatsApp |
| [#7](https://github.com/ryujiee/AI-assistent/pull/7) | `feat/finance-07-frontend` | Dashboard, transações, categorias e orçamentos |
| [#8](https://github.com/ryujiee/AI-assistent/pull/8) | `feat/finance-08-alerts` | Alertas e resumos agendados |
| [#9](https://github.com/ryujiee/AI-assistent/pull/9) | `feat/finance-09-hardening` | Logs `slog`, CSP, testes de privacidade, E2E, screenshots, documentação, este relatório |

## 3. Grafo de dependências

```
main ◄─ #1 foundation ◄─ #2 ledger ◄─ #3 whatsapp ◄─ #4 agent ◄─ #5 receipts ◄─ #6 queries ◄─ #7 frontend ◄─ #8 alerts ◄─ #9 hardening
```

A cadeia é linear: cada PR tem como base o branch anterior. Faça o merge na ordem, do #1 ao #9. Depois de mergear cada um, o próximo passa a apontar para `main`: o GitHub faz isso sozinho quando o branch-base é apagado; se não fizer, troque a base manualmente. Cada branch compila e passa nos testes isoladamente (verificado).

## 4. Migrations

| Versão | Aplicação | Conteúdo |
|---|---|---|
| `001_baseline` | automática no boot | as tabelas que a Secretária já criava no boot, todas com `IF NOT EXISTS`; em produção só registra a versão |
| `002_finance` | **manual**: `secretary migrate apply` | todas as tabelas `finance_*` |

- `secretary migrate status | apply [versão]`.
- A tabela `schema_migrations` guarda checksum; editar uma migration já aplicada bloqueia o `apply`.
- `FINANCE_ENABLED` só liga o módulo e **nunca** cria tabela. Se a migration estiver pendente, o módulo espera e registra um aviso no log.

## 5. Tabelas

| Tabela | Conteúdo |
|---|---|
| `finance_workspaces` | grupo vinculado (JID + nome), momento do vínculo, limiar de confiança, preferências de resumo |
| `finance_members` | JID, LID, telefone, nome; o bot fica de fora |
| `finance_categories` | um nível de subcategoria, ícone, tipo, essencialidade, orçamento mensal, arquivamento |
| `finance_transactions` | `amount_cents BIGINT`, moeda, tipo, status, data civil, pagador, origem, comprovante, confiança, pendência, duplicata, soft delete, `UNIQUE(source_inbox_id, source_item)` |
| `finance_transaction_events` | auditoria com before/after em JSONB, autor, canal, mensagem de origem, undo |
| `finance_inbox` | `UNIQUE(chat, remetente, id)`, estados, tentativas, erro, referência da mídia |
| `finance_attachments` | BYTEA, sha256 único por workspace, MIME validado, até 10 MB |
| `finance_alerts_sent` | deduplicação de alertas e resumos |

## 6. Rotas

**Públicas:**
- `GET /api/health`
- `GET /api/session`
- `POST /api/login`
- `POST /api/logout`

**Com sessão:**
- Secretária (inalteradas): `GET /api/status`, `POST /api/config`, `POST /api/qrcode/refresh`.
- `GET /api/finance/status`
- `GET|PATCH /api/finance/settings`
- `GET|POST /api/finance/categories` e `PATCH /api/finance/categories/{id}`
- `GET /api/finance/members` e `PATCH /api/finance/members/{id}`
- `GET|POST /api/finance/transactions`
- `GET|PATCH|DELETE /api/finance/transactions/{id}` e `POST /api/finance/transactions/{id}/restore`
- `GET /api/finance/overview`
- `GET /api/finance/budgets`
- `GET /api/finance/attachments/{id}`
- `GET /api/finance/whatsapp`
- `GET /api/finance/whatsapp/groups`
- `POST|DELETE /api/finance/whatsapp/group`
- `POST /api/finance/whatsapp/members/sync`

**Só com `WHATSAPP_FAKE`:**
- `POST /api/dev/whatsapp/messages`
- `GET /api/dev/whatsapp/outbox`

**Telas do painel:** `/login`, `/secretaria`, `/financeiro`, `/financeiro/transacoes`, `/financeiro/categorias`, `/financeiro/whatsapp`.

## 7. Ferramentas da IA

Todas são `strict`. Nenhuma recebe workspace ou conta, e argumentos desconhecidos são rejeitados.

**Mutação** (no máximo 5 por mensagem; alterar ou apagar só vale para transações do contexto):
- `create_transaction`
- `update_transaction`
- `delete_transaction`
- `undo_last_action`
- `set_category_budget`

**Consulta** (devolvem um relatório pronto, calculado pelo backend):
- `merchant_history`
- `financial_summary`
- `list_transactions`
- `compare_periods`
- `budget_status`
- `insights`

**Quando o backend confirma um lançamento.** Só fica CONFIRMED quando:
- o valor aparece na mensagem ou no comprovante;
- a categoria existe;
- a data é resolvida e não é futura;
- a confiança é maior ou igual ao limiar;
- não há suspeita de duplicata.

Caso contrário, fica PENDING e o bot faz uma única pergunta.

## 8. Fluxo WhatsApp

1. O router decide pelo chat, usando só metadados.
2. Mensagens de antes do vínculo, edições e mensagens do próprio bot são ignoradas.
3. O inbox é idempotente, com um worker ordenado por workspace.
4. O agente recebe um contexto compacto: 5 transações, 8 mensagens, categorias e calendário.
5. A resposta vai ao grupo citando a mensagem. O id da resposta fica ligado às transações, e é por ele que funciona a correção citando a confirmação.
6. Áudio passa pelo Whisper que já existia.
7. Conversa sem relação com finanças recebe silêncio.

## 9. Comprovantes

1. JPG, PNG, WEBP ou PDF, até 10 MB. Tipo e tamanho declarados são checados antes do download; depois, o tipo real é detectado pelos bytes.
2. Armazenamento privado em BYTEA.
3. Extração isolada, sem tools, com schema estrito. O schema não tem campo para CPF, chave PIX, agência ou conta. PDF vai por HTTP direto (stdlib).
4. Duplicidade: mesmo arquivo, mesmo ID PIX, ou mesmo valor + dia + recebedor. Nesse caso o bot pergunta antes de registrar.
5. O recebedor sozinho não define a categoria.

## 10. Idempotência

| Situação | Resultado |
|---|---|
| Evento reentregue ou reconexão | UNIQUE no inbox → no-op |
| Mesma mensagem processada de novo | UNIQUE `(source_inbox_id, source_item)` → não duplica |
| Processo morreu depois de gravar | é detectado pelo `source_inbox_id` → DONE sem reprocessar |
| Processo morreu no meio | PROCESSING há mais de 3 min volta a PENDING; depois de 3 tentativas, FAILED com aviso |

**Ajuste consciente:** o pedido era `source_inbox_id UNIQUE`. Implementei `(source_inbox_id, source_item)` para permitir "50 no mercado e 30 na farmácia" numa mensagem só, com a mesma garantia de não duplicar.

## 11. Frontend

- Vite 8, Vue 3.5, vue-router 5, TypeScript e Tailwind 3 com o tema original.
- A tela da Secretária foi preservada.
- Shell com abas Secretária/Financeiro e login.
- Gráficos em SVG próprio, sem dependência nova de gráficos.
- Paleta validada pela skill de dataviz nos tons escuros: CVD, contraste e faixa de luminosidade passam.

## 12. Dashboards

- Filtros de período, pessoa e tipo, que valem para a página inteira.
- KPIs: gastos (número principal), receitas, saldo e média diária, com comparação e projeção.
- Evolução acumulada do período atual contra o mesmo trecho do período anterior, com tooltip, teclado e visão em tabela.
- Destaques.
- Ranking de categorias com marca de orçamento.
- Essencial × discricionário.
- Quem pagou (com o aviso "não é quem deve a quem").
- Orçamentos.
- Maiores gastos.

## 13. Orçamentos

- Mensais, definidos na própria categoria. Os de categoria-pai somam as subcategorias.
- Edição inline na tela de categorias ou pelo grupo ("limite de 600 para restaurantes").
- Medidores com estado sinalizado por ícone e texto.

## 14. Alertas

- Vão junto da resposta do bot, no máximo 2 linhas.
- Orçamento a 70/80/100%, anunciando só o maior nível atingido.
- Categoria discricionária 30% e R$ 100 acima do mesmo período do mês anterior.
- Gasto fora do padrão, só com 8 ou mais gastos anteriores na categoria.
- Terceiro registro seguido da mesma categoria discricionária.
- Assinatura mais cara que no mês anterior.
- Deduplicação por chave (categoria/mês/limite, dia ou transação).
- Tom neutro.

## 15. Resumos

- Mensal: último dia às 20h ou dia 1 às 9h.
- Semanal: segundas às 9h.
- Os dois vêm desligados.
- Rodam no cron existente, com atraso aleatório de até 20 min.
- Cada um é enviado uma vez e nunca para um período vazio.

## 16. Segurança

Detalhes em [docs/finance/SECURITY.md](docs/finance/SECURITY.md). Em resumo:
- painel fechado, com cookie assinado e rate limit;
- checagem de Origin, CORS restrito e CSP;
- isolamento por workspace resolvido no servidor;
- tools estritas com travas no código;
- extração de comprovantes sem tools;
- PII removida antes de gravar;
- comprovantes privados;
- logs sem conteúdo.

## 17. Testes

| Suíte | Resultado |
|---|---|
| Go (`go test ./...`, SQL real no Postgres do compose) | 91 funções de teste, todas passando |
| Frontend (vitest) | 15 testes passando; `vue-tsc` limpo |
| `go vet ./...` | limpo |
| Build | backend, frontend e **imagem Docker de produção** ok |
| E2E local | login → grupo → "gastei 50 no mercado" → painel → "na verdade foi 60" → auditoria → "quanto gastamos esse mês?" → R$ 60 |
| `ui-qa` | sem overflow, texto cortado, rótulos sobrepostos ou erros de console/HTTP em 1440/1366/1024/390 |
| `npm audit` | 0 vulnerabilidades |
| Stack | cada um dos 9 branches compila e passa nos testes isoladamente |

O que os testes cobrem, por área:
- **Dinheiro e datas:** parser de valores, datas relativas e períodos.
- **Relatórios:** totais, filtros, projeção, série, orçamentos, insights e outliers.
- **Ledger:** confiança/pendência, duplicidade, resolução de categoria, auditoria e undo.
- **Idempotência:** mesma mensagem, crash depois da transação, PROCESSING abandonado, retry.
- **Router:** privado, grupo financeiro, outro grupo, mídia de outro grupo sem download.
- **Agente (LLM mock):** gasto, receita, transferência, "ontem", pendente, correção citada, undo e delete.
- **Comprovantes:** imagem, PDF, hash e ID PIX duplicados, mais de 10 MB, MIME inválido, tipo falsificado.
- **Segurança:** 401, CSRF, CORS, isolamento, `workspace_id` rejeitado, anexo de outro workspace, path traversal, prompt injection, PII fora dos logs.

## 18. Screenshots

Em [docs/finance/screenshots/](docs/finance/screenshots/), todas com dados fictícios (Ana e Bruno):

- **Baseline** (antes da migração): `baseline-connected`, `baseline-qr`.
- **Todas as telas** em 1440, 1366 e 1024: `login`, `secretaria`, `financeiro`, `financeiro-transacoes`, `financeiro-categorias`, `financeiro-whatsapp`.
- **Interações:**
  - `chart-tooltip`
  - `chart-evolution-card`
  - `financeiro-transacao-detalhe` (drawer com comprovante)
  - `financeiro-transacoes-pendentes`
  - `financeiro-whatsapp-modal`
  - `financeiro-categoria-modal`
  - `financeiro-390` (mobile)
- **Estados vazios:**
  - `empty-financeiro` (sem transações)
  - `empty-financeiro-transacoes`
  - `empty-financeiro-categorias` (sem orçamentos)
  - `empty-financeiro-whatsapp` (sem grupo)
  - `empty-finance-disabled` (módulo desligado)
  - `empty-finance-not-migrated` (migration pendente)
- **E2E:** `e2e-1-dashboard-50`, `e2e-2-drawer-corrected`.

**Atenção:** eu não consegui abrir as imagens para revisão visual. O hook `PreToolUse` da ferramenta de leitura travou durante toda a sessão. A validação visual foi automática:
- QA de layout no navegador;
- análise de pixels, que confirmou as linhas do gráfico e as cores de essencialidade.

Recomendo uma olhada humana nas screenshots.

## 19. Cobertura

Go, `go test -coverprofile`:

| Pacote | Cobertura |
|---|---|
| **total** | **59,9%** (antes: só `timeutil`) |
| `finance` | 76,6% |
| `web` | 62% |
| `timeutil` | 84,2% |
| `db` | 34,6% |
| `openai` | 21,6% |
| `whatsapp` | 21,0% |
| `engine` | 0% (código legado não tocado) |

Frontend: cobertura não medida (exigiria instalar `@vitest/coverage-v8`).

## 20. Pendências e decisões para você

**Decisões:**
1. **Mergear na ordem #1 → #9.**
2. **Antes do deploy do #1:** colocar `ADMIN_PASSWORD` (12+ caracteres) e `SESSION_SECRET` no `.env` do servidor. Sem isso, o painel fica bloqueado (a Secretária segue funcionando).
3. **Para ligar o financeiro:** backup, depois `docker exec secretary_backend ./secretary migrate apply`, depois `FINANCE_ENABLED=true`. Em seguida, criar o grupo com vocês dois e o número do bot e escolhê-lo no painel. Passo a passo em [docs/finance/SETUP.md](docs/finance/SETUP.md).

**Validações que não pude fazer:**

4. **OpenAI real não avaliada** (não havia chave local). Vale uma rodada curta com mensagens fictícias antes de usar para valer; a qualidade da interpretação e da leitura de comprovantes depende do modelo.
5. **WhatsApp real não testado:** listagem de grupos, nomes via contatos, envio com citação e LID em grupos. Tudo foi exercitado só com o gateway fake.
6. **Revisão visual das screenshots** (ver item 18).

**Recomendações de infraestrutura (não aplicadas):**

7. Publicar a porta 8000 só em `127.0.0.1` no compose.
8. HSTS no Nginx.
9. Backup do volume do Postgres.
10. Credenciais do Postgres/pgAdmin.

**Limites conhecidos:**

11. Mensagens editadas no WhatsApp são ignoradas.
12. Alertas não saem para lançamentos feitos pelo painel.
13. Um workspace por painel.
14. Projeções arredondam para R$ 10.

**Fora de escopo, como pedido:** integração bancária, Open Finance, cartão via API, divisão de despesas, cobrança, Pix automático, investimentos, câmbio, múltiplos workspaces e contas a pagar. O schema aceita PLANNED depois.

---

## Checklist de produto

`[x]` = implementado e testado localmente com o gateway fake. Nada foi validado em WhatsApp real (ver pendências).

- [x] selecionar grupo WhatsApp (modal com busca; salvo por JID)
- [x] identificar os dois membros (participantes → membros; nomes editáveis)
- [x] "gastei 50 no mercado"
- [x] "recebi 3 mil"
- [x] áudio (Whisper)
- [x] foto de comprovante
- [x] PDF
- [x] correção ("na verdade foi 60", categoria, data, pagador; por citação ou última transação)
- [x] undo ("desfaz")
- [x] delete ("apaga esse gasto", com volta)
- [x] consulta mensal
- [x] consulta por categoria
- [x] consulta por pessoa
- [x] comparação entre períodos
- [x] dashboard
- [x] categorias (árvore, ícone, essencialidade)
- [x] budgets (painel e WhatsApp)
- [x] alertas (orçamento, pico, fora do padrão, sequência, assinatura)
- [x] relatório (WhatsApp e painel)
- [x] resumo mensal (e semanal)
- [x] audit trail (before/after, quem, quando, mensagem)
- [x] attachment privado
- [x] idempotência
- [x] auth
- [x] isolamento por workspace
