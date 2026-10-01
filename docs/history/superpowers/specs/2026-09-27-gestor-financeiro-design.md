# Gestor Financeiro por IA — Design

Data: 2026-09-27 · Status: implementado em 9 PRs empilhados (não mergeados, não deployados).

## Objetivo

Um grupo privado de WhatsApp do casal como interface natural para registrar gastos, receitas e comprovantes e para perguntar sobre as finanças, com respostas baseadas nos dados reais; e uma área "Gestor Financeiro" no painel com dashboard, lançamentos, categorias e orçamentos. Tudo sobre a infraestrutura existente da Secretária de IA (Go + whatsmeow + go-openai + Postgres + Vue).

## Decisões do usuário

1. Bot continua com número próprio (separado dos dois).
2. Autenticação: `ADMIN_PASSWORD` com sessão por cookie assinado.
3. Pagamento de fatura do cartão é TRANSFER; as compras individuais são as despesas.
4. Frontend migrado para Vite + Vue 3 antes de crescer.
5. `FINANCE_ENABLED` liga o módulo; migrations versionadas e aplicadas manualmente.

## Princípios

- IA interpreta; backend valida, decide e grava. Nada de SQL livre, totais inventados ou valores estimados.
- Dinheiro em centavos (BIGINT). Datas civis no fuso da aplicação.
- Idempotência por mensagem; auditoria de toda mudança na mesma transação SQL.
- Perguntar só o que desbloqueia (categoria, confirmação, data); campos secundários opcionais.
- Tom neutro; conselhos só a partir de fatos (orçamento, variação, projeção), respeitando a essencialidade.

## Arquitetura

Descrita em `docs/finance/ARCHITECTURE.md`. Resumo: router por chat no `eventHandler` (metadados antes de mídia) → inbox idempotente com fila ordenada por workspace → agente com ferramentas estritas sobre o ledger → resposta curta no grupo citando a mensagem. Comprovantes passam por extração isolada sem ferramentas. Relatórios, alertas e resumos são calculados pelo backend.

## Escopo entregue (fases 0–23)

Baseline; autenticação; Vite; migration runner; modelo financeiro; inbox; router; grupo e membros; RunTools; agente (texto/áudio); contexto e correções; comprovantes; consultas; dashboard; alertas; resumos; testes; logs; UX/screenshots; documentação; PRs; validação combinada; relatório.

## Fora de escopo (preparado, não implementado)

Integração bancária/Open Finance, cartão via API, divisão de despesas, cobrança, Pix automático, investimentos, câmbio, múltiplos workspaces no painel, contas a pagar (o status aceita PLANNED depois), recorrências.

## Riscos residuais

- Qualidade da interpretação depende do modelo real (não avaliado com a OpenAI real: sem chave local). Mitigações: validação de evidência do valor, PENDING + pergunta, travas das ferramentas.
- Mensagens editadas no WhatsApp são ignoradas.
- Alertas só em resposta a mensagens do grupo.
- Porta 8000 exposta fora do Nginx (ver SECURITY.md).
