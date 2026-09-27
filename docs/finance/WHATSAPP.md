# Gestor Financeiro — Uso pelo WhatsApp

## Preparação

1. Crie um grupo no WhatsApp com vocês dois e o número conectado à Secretária (o bot continua sendo um número separado).
2. No painel: Financeiro → WhatsApp Financeiro → **Escolher grupo** → busque pelo nome → **Usar**.
3. Confira os nomes em "Quem é quem" (eles aparecem nos relatórios e nas respostas).

O vínculo usa o identificador interno do grupo (JID), então renomear o grupo não quebra nada. Só mensagens enviadas **depois** do vínculo são lidas; trocar de grupo não importa histórico. Outros grupos e conversas privadas não entram no financeiro (o privado com o número configurado continua indo para a Secretária).

## Registrar

| Mensagem | Resultado |
|---|---|
| "gastei 42 no almoço" | ✅ R$ 42,00 · Restaurantes · hoje |
| "paguei 250 de gasolina ontem" | ✅ R$ 250,00 · Combustível · ontem |
| "recebi 3 mil de salário" | 💰 R$ 3.000,00 · Salário · hoje |
| "passei 500 da conta A para a B" | ✅ R$ 500,00 · transferência (não conta como gasto) |
| "paguei a fatura do cartão, 1.200" | transferência — as compras do cartão já são lançadas uma a uma |
| "gastei 50 no mercado e 30 na farmácia" | dois lançamentos |
| "gastei 80" | 💬 R$ 80,00 anotado. Foi com o quê? → responda "mercado" |
| áudio: "gastei 25 no mercado" | transcrito (Whisper) e registrado |
| foto/print/PDF de comprovante | lido, registrado; se a categoria for ambígua, o bot pergunta |

Formatos de valor aceitos: `37,90`, `37.90`, `3.000`, `3 mil`, `R$ 89,99`, `37 reais e 90 centavos`. O valor precisa estar escrito na mensagem (ou no comprovante); se o modelo "entender" outro número, o lançamento fica pendente e o bot pede confirmação.

Datas: "hoje", "ontem", "anteontem", "sexta", "sexta passada", "dia 10", "10/09". Datas futuras não são aceitas.

Comprovante repetido (mesmo arquivo, mesmo ID PIX ou mesmo valor/dia/recebedor): "Esse comprovante parece já ter sido registrado como R$ 89,90 em Mercado (ontem). Deseja registrar novamente?" — responda "sim" para registrar.

## Corrigir

Responda (citando) a confirmação do bot — é a referência mais segura — ou escreva logo depois:

- "na verdade foi 97"
- "coloca em mercado"
- "era de ontem"
- "foi pago pela Ana" / "foi minha esposa"
- "apaga esse gasto" (dá para voltar com "desfaz")
- "desfaz" (desfaz sua última ação; não desfaz se outra pessoa alterou depois)

Toda correção fica no histórico do lançamento (antes/depois, quem, quando, mensagem de origem).

## Perguntar

- "quanto gastamos esse mês?" / "e mês passado?"
- "quanto deu mercado?" / "e restaurante?"
- "quanto gastamos entre 01/09 e 15/09?" / "de 10 a 20"
- "quanto eu gastei?" / "quanto minha esposa gastou?" / "quanto cada um gastou?"
- "qual nosso maior gasto?" / "lista os últimos 10 gastos"
- "compara esse mês com agosto"
- "onde estamos gastando mais?" / "estamos exagerando em alguma categoria?"
- "limite de 600 para restaurantes" (define orçamento)

As respostas usam só números calculados pelo backend. "Quem pagou" mostra quem pagou, não quem deve para quem. Comparações do mês corrente usam os mesmos dias do mês anterior. Projeções são rotuladas como projeção.

## Alertas e resumos

- Junto da resposta a um lançamento podem vir até dois avisos: orçamento em 70/80/100%, categoria discricionária bem acima do mês anterior, valor fora do padrão, terceiro registro seguido da mesma categoria, assinatura mais cara. Cada aviso sai uma vez (por mês, dia ou lançamento).
- Resumo mensal (último dia ou dia 1) e semanal (segundas), desligados por padrão; ligue em WhatsApp Financeiro → Preferências.

## Conversa normal

Mensagens sem relação com finanças ("ok", "te amo", figurinhas) são ignoradas: o bot não responde.

## Limites

- Mensagem editada no WhatsApp não altera o lançamento; mande a correção.
- Um grupo financeiro por painel.
- Sem integração bancária, cartão via API ou divisão de despesas.
