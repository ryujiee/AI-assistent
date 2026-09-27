# Gestor Financeiro — Arquitetura

Módulo financeiro da Secretária de IA. Um grupo de WhatsApp do casal é a interface; o painel web mostra dashboard, lançamentos, categorias e orçamentos. Tudo roda no mesmo backend Go, no mesmo Postgres e na mesma sessão whatsmeow da Secretária. Não existe infraestrutura paralela.

Princípio central: **a IA interpreta, o backend decide e grava**. O modelo escolhe ferramentas com argumentos estruturados; cada ferramenta valida tudo, verifica posse (workspace) e chama um serviço determinístico. O modelo nunca escreve SQL, nunca soma valores e nunca inventa totais.

## Componentes

```
backend/
  main.go              wiring (router, ingestor, agente, cron, HTTP)
  migrate_cmd.go       secretary migrate status | apply
  config/              variáveis de ambiente
  db/                  pool pgx, runner de migrations, migrations/*.sql, dbtest (schema isolado para testes)
  whatsapp/
    client.go          sessão whatsmeow, QR, eventHandler
    message.go         extração de metadados, router (Decide/Dispatch), download sob demanda
    gateway.go         RealGateway: grupos, participantes, envio com citação, download
    fake.go            WHATSAPP_FAKE: gateway fictício para desenvolvimento
  openai/
    client.go          Secretária (inalterada, agora usa RunTools)
    tools.go           RunTools: loop de function calling reutilizado
    structured.go      chamada com JSON schema estrito e arquivo (imagem/PDF), sem tools
  finance/
    model.go           tipos e Service
    money.go dates.go text.go   parser de valores, datas relativas, períodos, limpeza de PII
    workspace.go categories.go  workspace, membros, categorias, seed
    ledger.go views.go          criação/edição/exclusão/undo com auditoria; listagens
    inbox.go groups.go          ingestão idempotente, fila ordenada por workspace, vínculo do grupo
    agent.go agent_tools.go context.go   agente financeiro e ferramentas
    receipts.go attachments.go  comprovantes
    reports.go insights.go report_tools.go   relatórios e ferramentas de consulta
    alerts.go summaries.go      alertas e resumos agendados
    fakellm.go                  OPENAI_FAKE: modelo por regras para desenvolvimento
  web/
    auth.go            login, sessão assinada, rate limit, Origin/CORS
    server.go          rotas, SPA, cabeçalhos de segurança
    finance.go finance_whatsapp.go   API /api/finance/*
frontend/              Vite + Vue 3 + TypeScript + Tailwind 3
  src/app              shell, login, rotas, sessão
  src/features/secretary  tela original da Secretária
  src/features/finance    overview/, transactions/, categories/, whatsapp/, charts/, components/, lib/
```

## Fluxo de uma mensagem

```
whatsmeow events.Message
  └─ ExtractMessage: chat, remetente (PN/LID), id, horário, nome, citação, tipo, referência da mídia (sem baixar)
      └─ Decide
          ├─ chat privado do número configurado -> Secretária (fluxo original: baixa mídia, Whisper, OpenAI)
          ├─ grupo vinculado ao financeiro      -> Ingestor.Accept
          └─ qualquer outro chat               -> ignorado (nada é baixado)

Ingestor.Accept (síncrono, só banco)
  - ignora mensagens anteriores ao vínculo do grupo, edições e mensagens do próprio bot
  - registra/atualiza o membro (PN, LID, nome)
  - INSERT finance_inbox ON CONFLICT (chat, remetente, id) DO NOTHING  -> reentrega = no-op
  - acorda o worker do workspace

Worker do workspace (um por workspace, em ordem de chegada)
  - reivindica a cabeça da fila (PENDING -> PROCESSING, attempts+1)
  - se a mensagem já alterou o ledger (transação ou evento com esse inbox) -> DONE sem reprocessar
  - Agent.Handle:
      áudio  -> download -> Whisper -> texto (guardado no inbox)
      imagem/PDF -> ReceiptReader (download, validação, armazenamento, extração sem tools, duplicidade)
      monta contexto compacto -> RunTools com ferramentas estritas -> resposta
  - resposta enviada ao grupo citando a mensagem; id da resposta ligado ao inbox e às transações
  - DONE; erro transitório -> PENDING com backoff (5 s, 30 s), no máximo 3 tentativas -> FAILED com aviso
  - erro permanente (arquivo inválido etc.) -> FAILED com resposta explicando
```

## Idempotência

| Camada | Mecanismo |
|---|---|
| Evento reentregue pelo WhatsApp / reconexão | `UNIQUE (chat_jid, sender_jid, wa_message_id)` em `finance_inbox` |
| Mesma mensagem gerando transação duas vezes | `UNIQUE (source_inbox_id, source_item)` em `finance_transactions` |
| Processo morreu depois de gravar | antes de processar: existe transação ou evento com `source_inbox_id`? então DONE |
| Processo morreu no meio (sem efeito) | `Recover` a cada 30 s: PROCESSING há mais de 3 min volta para PENDING (ou FAILED após 3 tentativas) |
| Retry infinito | `MaxAttempts = 3` |

`source_item` existe porque uma mensagem pode listar vários gastos ("50 no mercado e 30 na farmácia"). O pedido original era `source_inbox_id UNIQUE`; a chave composta mantém a garantia (a mesma mensagem nunca cria o mesmo item duas vezes) e permite vários itens criados atomicamente.

## Modelo de dados (migration 002)

- `finance_workspaces`: grupo vinculado (JID estável + nome em cache), horário do vínculo, limiar de confiança, preferências de resumo.
- `finance_members`: identidade WhatsApp (JID, LID, telefone) e nome exibido; o bot nunca vira membro.
- `finance_categories`: árvore de um nível, ícone, tipo (despesa/receita), essencialidade, orçamento mensal (orçamento na própria categoria, sem tabela extra), arquivamento.
- `finance_transactions`: `amount_cents BIGINT`, `currency`, tipo (EXPENSE, INCOME, TRANSFER, REFUND), status (CONFIRMED, PENDING), `transaction_date DATE` (dia civil em America/Sao_Paulo), pagador, autor, origem, comprovante, confiança, motivo de pendência, possível duplicata, soft delete.
- `finance_transaction_events`: auditoria com `before`/`after` em JSONB, autor, canal, mensagem de origem, gravada na mesma transação SQL da mudança; `undone_by_event_id` para desfazer.
- `finance_inbox`: mensagens aceitas e seu estado.
- `finance_attachments`: comprovantes (BYTEA, sha256 único por workspace).
- `finance_alerts_sent`: deduplicação de alertas e resumos.

Regras de relatório: gasto = EXPENSE − REFUND; TRANSFER nunca conta (inclui pagamento de fatura do cartão, porque as compras já são lançadas uma a uma); PENDING e excluídas ficam fora. Totais são sempre calculados na hora; nenhum total é armazenado.

## Agente financeiro

Contexto enviado ao modelo por mensagem: regras, data de hoje com calendário dos últimos 8 dias, membros, categorias do banco, até 5 transações recentes (pendentes primeiro), a transação citada (se houver), a última transação de quem escreveu (30 min) e as últimas 8 mensagens do grupo. Nunca o histórico financeiro inteiro.

Ferramentas (todas `strict`, nenhuma recebe workspace ou conta; argumentos desconhecidos são rejeitados):

| Ferramenta | O que faz |
|---|---|
| `create_transaction` | 1 a 5 itens; o backend decide CONFIRMED ou PENDING |
| `update_transaction` | corrige valor, categoria, data, pagador, descrição, tipo; `confirm` para respostas do usuário |
| `delete_transaction` | soft delete |
| `undo_last_action` | desfaz a última ação de quem escreveu (bloqueia se outra pessoa mudou depois) |
| `set_category_budget` | define/remove orçamento |
| `merchant_history` | categorias usadas antes para um recebedor |
| `financial_summary` | totais, categorias, pessoas, comparação, projeção |
| `list_transactions` | últimos ou maiores lançamentos |
| `compare_periods` | dois períodos com maiores variações |
| `budget_status` | orçamentos do mês |
| `insights` | fatos do período (orçamentos, variações, projeção, fora do padrão) |

Travas independentes do prompt:
- `update`/`delete` só aceitam transações que apareceram no contexto ou foram retornadas por consulta na mesma conversa.
- no máximo 5 alterações por mensagem;
- toda operação é escopada pelo workspace resolvido no servidor.

Quando uma transação é CONFIRMED: o valor precisa estar escrito na mensagem (ou ser o valor lido do comprovante), a categoria precisa existir, a data precisa ser resolvida e não pode ser futura, a confiança precisa ser ≥ limiar do workspace e não pode haver suspeita de duplicata. Caso contrário fica PENDING e o bot faz uma única pergunta ("Foi com o quê?", "Confirma?", "Qual foi o dia?", "Deseja registrar novamente?"). Datas relativas ("ontem", "sexta", "dia 10") são resolvidas em código, não pelo modelo.

## Comprovantes

1. Tipo e tamanho declarados são checados antes do download (JPG, PNG, WEBP, PDF; até 10 MB).
2. Após o download, o tipo real é detectado pelos bytes.
3. O arquivo é guardado em `finance_attachments` (privado, uma cópia por sha256).
4. Uma chamada separada, **sem ferramentas**, extrai campos para um JSON schema estrito. PDFs vão como parte `file` numa chamada HTTP direta (o SDK não suporta), imagens como `image_url`.
5. O schema não tem campo para CPF, chave PIX, agência ou conta; texto livre é limpo e só um ID E2E de PIX bem formado é mantido.
6. Duplicidade: mesmo arquivo ou mesmo ID PIX (score 1), mesmo valor + dia (±1) + recebedor parecido (0,8). A partir de 0,8 o bot pergunta antes.
7. O agente recebe só os campos extraídos, rotulados como dados não confiáveis.

## Alertas e resumos

- Alertas vão junto da resposta do bot (no máximo 2 linhas): orçamento 70/80/100% (só o maior nível atingido), categoria discricionária 30% e R$ 100 acima do mesmo período do mês anterior, gasto fora do padrão (só com 8+ gastos anteriores na categoria), terceiro registro da mesma categoria discricionária desde ontem, assinatura mais cara que no mês anterior. Cada alerta tem chave em `finance_alerts_sent` (por categoria/mês/limite, por dia ou por transação).
- Resumo mensal (último dia às 20h ou dia 1 às 9h) e semanal (segunda às 9h), desligados por padrão, com atraso aleatório de até 20 min, enviados uma vez e nunca para período vazio. Rodam no cron existente (`engine.AddJob`).

## Painel

- `/login`, `/secretaria` (tela original), `/financeiro` (visão geral), `/financeiro/transacoes`, `/financeiro/categorias`, `/financeiro/whatsapp`.
- Gráficos em SVG próprio (sem biblioteca): linha acumulada atual × período anterior com tooltip, teclado e tabela; ranking de categorias em barras de um tom; barra de essencialidade; medidores de orçamento. Paleta validada para daltonismo e contraste (dataviz).
- API: `/api/finance/{status,settings,categories,members,transactions,overview,budgets,attachments/{id},whatsapp,...}`, sempre atrás da sessão.

## Decisões e limites conhecidos

- Um workspace por painel (o admin vê o workspace padrão). O modelo já separa tudo por `workspace_id`; múltiplos workspaces exigiriam mapear sessão → workspace.
- Fuso fixo America/Sao_Paulo via `timeutil` (decisão já existente no projeto).
- Mensagens editadas no WhatsApp são ignoradas; a correção é feita com nova mensagem.
- Alertas só saem em resposta a mensagens do grupo (lançamentos pelo painel não disparam mensagem no grupo).
- Contas a pagar (PLANNED), recorrências, divisão de despesas, câmbio e integração bancária ficaram fora; o esquema aceita PLANNED sem migração destrutiva.
