package finance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

// Tool schemas. Every tool is strict (the model must match the schema) and
// none takes a workspace or account: the server injects them. Arguments are
// still validated here, because the model's output is untrusted input.

func strictTool(name, description string, props map[string]any) sashabaranov_openai.Tool {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	return sashabaranov_openai.Tool{Type: sashabaranov_openai.ToolTypeFunction, Function: &sashabaranov_openai.FunctionDefinition{
		Name: name, Description: description, Strict: true,
		Parameters: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
	}}
}

func sString(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func sNullString(desc string) map[string]any {
	return map[string]any{"type": []string{"string", "null"}, "description": desc}
}
func sInt(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func sNullInt(desc string) map[string]any {
	return map[string]any{"type": []string{"integer", "null"}, "description": desc}
}
func sEnum(desc string, values []string, nullable bool) map[string]any {
	vals := make([]any, 0, len(values)+1)
	for _, v := range values {
		vals = append(vals, v)
	}
	typ := any("string")
	if nullable {
		vals = append(vals, nil)
		typ = []string{"string", "null"}
	}
	return map[string]any{"type": typ, "enum": vals, "description": desc}
}

func periodProps(extra map[string]any) map[string]any {
	p := map[string]any{
		"period": sEnum("Período. 'month' usa month (AAAA-MM); 'custom' usa start e end (AAAA-MM-DD).", PeriodKeys, false),
		"month":  sNullString("AAAA-MM quando period = month"),
		"start":  sNullString("AAAA-MM-DD quando period = custom"),
		"end":    sNullString("AAAA-MM-DD quando period = custom"),
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func mutationTools() []sashabaranov_openai.Tool {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":           sEnum("EXPENSE gasto; INCOME receita; TRANSFER entre contas próprias ou pagamento de fatura do cartão; REFUND estorno de uma compra", TransactionTypes, false),
			"amount_cents":   sInt("Valor em centavos exatamente como escrito (R$ 37,90 = 3790; 3 mil = 300000)"),
			"description":    sString("Descrição curta em português (ex: Compra no mercado)"),
			"category":       sNullString("Nome exato de uma categoria da lista; null se não tiver certeza"),
			"merchant":       sNullString("Estabelecimento ou recebedor, se dito; senão null"),
			"date":           sNullString("Expressão do usuário: hoje, ontem, anteontem, sexta, dia 10, 10/09 ou AAAA-MM-DD; null = hoje"),
			"payer":          sNullString("Nome do membro que pagou; null = quem enviou a mensagem"),
			"payment_method": sEnum("Forma de pagamento se dita; senão null", PaymentMethods, true),
			"confidence":     map[string]any{"type": "number", "description": "Confiança de 0 a 1 na interpretação"},
		},
		"required":             []string{"type", "amount_cents", "description", "category", "merchant", "date", "payer", "payment_method", "confidence"},
		"additionalProperties": false,
	}
	return []sashabaranov_openai.Tool{
		strictTool("create_transaction", "Registra um ou mais gastos, receitas, transferências ou estornos citados na mensagem (máximo 5).",
			map[string]any{"items": map[string]any{"type": "array", "items": item}}),
		strictTool("update_transaction", "Corrige uma transação do contexto. Envie null nos campos que não mudam.", map[string]any{
			"transaction_id": sInt("Id (#) de uma transação do contexto"),
			"amount_cents":   sNullInt("Novo valor em centavos"),
			"category":       sNullString("Nova categoria (nome da lista)"),
			"date":           sNullString("Nova data (expressão do usuário ou AAAA-MM-DD)"),
			"payer":          sNullString("Quem pagou (nome do membro)"),
			"description":    sNullString("Nova descrição"),
			"merchant":       sNullString("Novo estabelecimento"),
			"type":           sEnum("Novo tipo", TransactionTypes, true),
			"confirm":        map[string]any{"type": "boolean", "description": "true quando o usuário respondeu a uma pergunta ou confirmou explicitamente"},
		}),
		strictTool("delete_transaction", "Apaga uma transação do contexto (pode ser desfeito).", map[string]any{
			"transaction_id": sInt("Id (#) de uma transação do contexto"),
		}),
		strictTool("undo_last_action", "Desfaz a última ação de quem escreveu (registro, correção ou exclusão).", map[string]any{}),
		strictTool("set_category_budget", "Define ou remove o orçamento mensal de uma categoria de despesa.", map[string]any{
			"category":             sString("Nome da categoria"),
			"monthly_budget_cents": sNullInt("Limite mensal em centavos; null remove o orçamento"),
		}),
		strictTool("merchant_history", "Mostra em quais categorias um estabelecimento/recebedor já foi registrado.", map[string]any{
			"merchant": sString("Nome do estabelecimento ou recebedor"),
		}),
	}
}

// maxMutations bounds how much one message can change the ledger.
const maxMutations = 5

// turn holds the state of one message being processed: who wrote it, what
// the tools may touch, and what they touched.
type turn struct {
	svc          *Service
	ws           *Workspace
	actor        Actor
	sender       *Member
	text         string
	source       string
	receipt      *Receipt
	attachmentID *int64
	duplicateOf  *int64
	cats         []Category
	byCat        map[int64]*Category

	allowed   map[int64]bool
	mutations int
	items     int
	touched   []int64
}

type toolResult struct {
	OK      bool   `json:"ok"`
	Reply   string `json:"reply,omitempty"`
	Error   string `json:"error,omitempty"`
	Details any    `json:"details,omitempty"`
}

func (r toolResult) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func fail(msg string) string { return toolResult{OK: false, Error: msg}.String() }

// decodeArgs rejects any field the schema does not define (a workspace_id,
// an account id...).
func decodeArgs(args string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("argumentos inválidos para a ferramenta")
	}
	return nil
}

func (t *turn) execute(ctx context.Context, name, args string) string {
	switch name {
	case "create_transaction":
		return t.createTransaction(ctx, args)
	case "update_transaction":
		return t.updateTransaction(ctx, args)
	case "delete_transaction":
		return t.deleteTransaction(ctx, args)
	case "undo_last_action":
		return t.undo(ctx, args)
	case "set_category_budget":
		return t.setBudget(ctx, args)
	case "merchant_history":
		return t.merchantHistory(ctx, args)
	}
	if out, ok := t.runReportTool(ctx, name, args); ok {
		return out
	}
	return fail("ferramenta desconhecida")
}

func (t *turn) spend(n int) error {
	if t.mutations+n > maxMutations {
		return fmt.Errorf("limite de %d alterações por mensagem atingido", maxMutations)
	}
	t.mutations += n
	return nil
}

func (t *turn) allow(ids ...int64) {
	for _, id := range ids {
		t.allowed[id] = true
	}
}

func (t *turn) categoryLabel(id *int64) string {
	if id == nil {
		return "sem categoria"
	}
	if c, ok := t.byCat[*id]; ok {
		return c.Name
	}
	return "sem categoria"
}

func (t *turn) resolveCategory(name, txType string) (*Category, []string) {
	kind := KindExpense
	if txType == TypeIncome {
		kind = KindIncome
	}
	return resolveCategory(t.cats, name, kind)
}

// confirmation renders the short line sent after a change.
func (t *turn) confirmation(tx *Transaction) string {
	parts := []string{FormatBRL(tx.AmountCents)}
	switch tx.Type {
	case TypeTransfer:
		parts = append(parts, "transferência")
	case TypeRefund:
		parts = append(parts, "estorno em "+t.categoryLabel(tx.CategoryID))
	default:
		parts = append(parts, t.categoryLabel(tx.CategoryID))
	}
	parts = append(parts, HumanDate(tx.TransactionDate, t.svc.now()))
	if tx.PayerMemberID != nil && (t.sender == nil || *tx.PayerMemberID != t.sender.ID) {
		if m, err := t.svc.GetMember(context.Background(), t.ws.ID, *tx.PayerMemberID); err == nil {
			parts = append(parts, "pago por "+m.DisplayName)
		}
	}
	if tx.Type == TypeIncome {
		return "💰 " + strings.Join(parts, " · ")
	}
	return "✅ " + strings.Join(parts, " · ")
}

type createItem struct {
	Type          string  `json:"type"`
	AmountCents   int64   `json:"amount_cents"`
	Description   string  `json:"description"`
	Category      *string `json:"category"`
	Merchant      *string `json:"merchant"`
	Date          *string `json:"date"`
	Payer         *string `json:"payer"`
	PaymentMethod *string `json:"payment_method"`
	Confidence    float64 `json:"confidence"`
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// amountHasEvidence is the anti-hallucination check: an amount must be
// written in the message, or match the amount read from the receipt.
func (t *turn) amountHasEvidence(cents int64) bool {
	if t.receipt != nil && t.receipt.AmountCents != nil && *t.receipt.AmountCents == cents {
		return true
	}
	return AmountInText(cents, t.text)
}

func (t *turn) createTransaction(ctx context.Context, args string) string {
	var a struct {
		Items []createItem `json:"items"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	if len(a.Items) == 0 {
		return fail("nenhum item informado")
	}
	if err := t.spend(len(a.Items)); err != nil {
		return fail(err.Error())
	}

	var inputs []TxInput
	var questions []string
	for _, it := range a.Items {
		in := TxInput{
			Type: it.Type, AmountCents: it.AmountCents, Description: it.Description, Merchant: deref(it.Merchant),
			PaymentMethod: deref(it.PaymentMethod), Source: t.source, SourceItem: t.items, AttachmentID: t.attachmentID,
			AIConfidence: ptr(it.Confidence), PossibleDuplicateOf: t.duplicateOf,
		}
		t.items++
		if !contains(TransactionTypes, in.Type) {
			return fail("tipo inválido")
		}
		if t.receipt != nil && t.receipt.ExternalRef != nil {
			in.ExternalRef = *t.receipt.ExternalRef
		}

		if in.Type != TypeTransfer {
			if it.Category != nil && strings.TrimSpace(*it.Category) != "" {
				if c, _ := t.resolveCategory(*it.Category, in.Type); c != nil {
					in.CategoryID = &c.ID
				}
			}
			if in.CategoryID == nil {
				in.PendingReasons = append(in.PendingReasons, ReasonCategoryMissing)
			}
		}

		date, err := ResolveDate(deref(it.Date), t.svc.now())
		switch {
		case errors.Is(err, ErrFutureDate):
			return fail("a data informada está no futuro; pergunte ao usuário a data correta")
		case err != nil:
			date, _ = ResolveDate("", t.svc.now())
			in.PendingReasons = append(in.PendingReasons, ReasonDateUnclear)
		}
		in.Date = date

		payer := t.actor.MemberID
		if it.Payer != nil {
			if p, err := t.svc.ResolvePayer(ctx, t.ws.ID, t.actor.MemberID, *it.Payer); err == nil {
				payer = p
			}
		}
		in.PayerMemberID = payer

		if it.Confidence < t.ws.ConfidenceThreshold {
			in.PendingReasons = append(in.PendingReasons, ReasonLowConfidence)
		}
		if !t.amountHasEvidence(in.AmountCents) {
			in.PendingReasons = append(in.PendingReasons, ReasonAmountUnchecked)
		}
		if t.duplicateOf != nil {
			in.PendingReasons = append(in.PendingReasons, ReasonDuplicate)
		}
		inputs = append(inputs, in)
	}

	txs, err := t.svc.CreateTransactions(ctx, t.ws.ID, t.actor, inputs)
	if err != nil {
		var v *ValidationError
		if errors.As(err, &v) {
			return fail(v.Msg)
		}
		return fail("não foi possível registrar agora")
	}

	var replies []string
	var details []map[string]any
	for i := range txs {
		tx := &txs[i]
		t.allow(tx.ID)
		t.touched = append(t.touched, tx.ID)
		line, question := t.describeCreated(tx)
		replies = append(replies, line)
		if question != "" {
			questions = append(questions, question)
		}
		details = append(details, map[string]any{"id": tx.ID, "status": tx.Status})
	}
	reply := strings.Join(replies, "\n")
	if len(questions) > 0 {
		reply += "\n" + strings.Join(questions, "\n")
	}
	return toolResult{OK: true, Reply: reply, Details: details}.String()
}

// describeCreated returns the confirmation line, or the pending line plus
// the single question that unblocks it.
func (t *turn) describeCreated(tx *Transaction) (string, string) {
	if tx.Status == StatusConfirmed {
		return t.confirmation(tx), ""
	}
	reasons := ""
	if tx.PendingReason != nil {
		reasons = *tx.PendingReason
	}
	amount := FormatBRL(tx.AmountCents)
	switch {
	case strings.Contains(reasons, ReasonDuplicate):
		if tx.PossibleDuplicateOf != nil {
			if prev, err := t.svc.GetTransaction(context.Background(), t.ws.ID, *tx.PossibleDuplicateOf, true); err == nil {
				return fmt.Sprintf("⚠️ Esse comprovante parece já ter sido registrado como %s em %s (%s).",
						FormatBRL(prev.AmountCents), t.categoryLabel(prev.CategoryID), HumanDate(prev.TransactionDate, t.svc.now())),
					"Deseja registrar novamente?"
			}
		}
		return "⚠️ Esse comprovante parece já ter sido registrado.", "Deseja registrar novamente?"
	case strings.Contains(reasons, ReasonCategoryMissing):
		if t.receipt != nil {
			return fmt.Sprintf("🧾 Identifiquei %s, mas não consegui saber a categoria.", amount), "Foi com o quê? (ex.: Mercado, Restaurantes, Outros)"
		}
		if tx.Type == TypeIncome {
			return fmt.Sprintf("💬 %s anotado.", amount), "Foi de quê? (ex.: Salário, Renda extra)"
		}
		return fmt.Sprintf("💬 %s anotado.", amount), "Foi com o quê?"
	case strings.Contains(reasons, ReasonDateUnclear):
		return fmt.Sprintf("💬 %s · %s anotado.", amount, t.categoryLabel(tx.CategoryID)), "Qual foi o dia?"
	default:
		return fmt.Sprintf("💬 Entendi %s · %s · %s.", amount, t.categoryLabel(tx.CategoryID), HumanDate(tx.TransactionDate, t.svc.now())), "Confirma?"
	}
}

type updateArgs struct {
	TransactionID int64   `json:"transaction_id"`
	AmountCents   *int64  `json:"amount_cents"`
	Category      *string `json:"category"`
	Date          *string `json:"date"`
	Payer         *string `json:"payer"`
	Description   *string `json:"description"`
	Merchant      *string `json:"merchant"`
	Type          *string `json:"type"`
	Confirm       bool    `json:"confirm"`
}

func (t *turn) updateTransaction(ctx context.Context, args string) string {
	var a updateArgs
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	if !t.allowed[a.TransactionID] {
		return fail("essa transação não está no contexto desta conversa; peça ao usuário para indicar qual é (ou responder à mensagem de confirmação)")
	}
	current, err := t.svc.GetTransaction(ctx, t.ws.ID, a.TransactionID, false)
	if err != nil {
		return fail("transação não encontrada")
	}
	if err := t.spend(1); err != nil {
		return fail(err.Error())
	}

	patch := TxPatch{Confirm: a.Confirm, Description: a.Description, Merchant: a.Merchant}
	txType := current.Type
	if a.Type != nil {
		if !contains(TransactionTypes, *a.Type) {
			return fail("tipo inválido")
		}
		patch.Type, txType = a.Type, *a.Type
	}
	if a.AmountCents != nil {
		if !t.amountHasEvidence(*a.AmountCents) {
			return fail("o novo valor não aparece na mensagem; confirme o valor com o usuário")
		}
		patch.AmountCents = a.AmountCents
	}
	if a.Category != nil && txType != TypeTransfer {
		c, options := t.resolveCategory(*a.Category, txType)
		if c == nil {
			return fail("categoria desconhecida; opções: " + strings.Join(options, ", "))
		}
		patch.CategoryID = &c.ID
	}
	if a.Date != nil {
		d, err := ResolveDate(*a.Date, t.svc.now())
		if err != nil {
			return fail("data inválida (" + err.Error() + "); pergunte o dia ao usuário")
		}
		patch.Date = &d
	}
	if a.Payer != nil {
		p, err := t.svc.ResolvePayer(ctx, t.ws.ID, t.actor.MemberID, *a.Payer)
		if err != nil {
			return fail(err.Error())
		}
		patch.PayerMemberID = p
	}

	tx, changed, err := t.svc.UpdateTransaction(ctx, t.ws.ID, a.TransactionID, t.actor, patch)
	if err != nil {
		var v *ValidationError
		if errors.As(err, &v) {
			return fail(v.Msg)
		}
		return fail("não foi possível corrigir agora")
	}
	t.touched = append(t.touched, tx.ID)
	if !changed {
		return toolResult{OK: true, Reply: "Nada mudou: " + strings.TrimPrefix(t.confirmation(tx), "✅ ")}.String()
	}
	line, question := t.describeCreated(tx)
	if tx.Status == StatusConfirmed {
		line = "✏️ Corrigido: " + strings.TrimPrefix(strings.TrimPrefix(line, "✅ "), "💰 ")
	}
	if question != "" {
		line += "\n" + question
	}
	return toolResult{OK: true, Reply: line}.String()
}

func (t *turn) deleteTransaction(ctx context.Context, args string) string {
	var a struct {
		TransactionID int64 `json:"transaction_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	if !t.allowed[a.TransactionID] {
		return fail("essa transação não está no contexto desta conversa; peça ao usuário para indicar qual é")
	}
	if err := t.spend(1); err != nil {
		return fail(err.Error())
	}
	tx, err := t.svc.DeleteTransaction(ctx, t.ws.ID, a.TransactionID, t.actor)
	if err != nil {
		return fail("transação não encontrada")
	}
	return toolResult{OK: true, Reply: fmt.Sprintf("🗑️ Apagado: %s · %s · %s. (diga \"desfaz\" para voltar)",
		FormatBRL(tx.AmountCents), t.categoryLabel(tx.CategoryID), HumanDate(tx.TransactionDate, t.svc.now()))}.String()
}

func (t *turn) undo(ctx context.Context, args string) string {
	var a struct{}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	if err := t.spend(1); err != nil {
		return fail(err.Error())
	}
	r, err := t.svc.UndoLast(ctx, t.ws.ID, t.actor)
	switch {
	case errors.Is(err, ErrNothingToUndo):
		return toolResult{OK: true, Reply: "Não encontrei nada recente seu para desfazer."}.String()
	case errors.Is(err, ErrUndoStale):
		return toolResult{OK: true, Reply: "Não dá para desfazer: esse registro foi alterado depois por outra pessoa."}.String()
	case err != nil:
		return fail("não foi possível desfazer agora")
	}
	tx := r.Transaction
	t.touched = append(t.touched, tx.ID)
	label := fmt.Sprintf("%s · %s", FormatBRL(tx.AmountCents), t.categoryLabel(tx.CategoryID))
	switch r.Reverted {
	case "CREATE":
		return toolResult{OK: true, Reply: "↩️ Desfeito: o registro de " + label + " foi removido."}.String()
	case "DELETE":
		return toolResult{OK: true, Reply: "↩️ Desfeito: " + label + " voltou."}.String()
	}
	return toolResult{OK: true, Reply: "↩️ Desfeito: voltou para " + label + " · " + HumanDate(tx.TransactionDate, t.svc.now()) + "."}.String()
}

func (t *turn) setBudget(ctx context.Context, args string) string {
	var a struct {
		Category string `json:"category"`
		Cents    *int64 `json:"monthly_budget_cents"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	c, options := resolveCategory(t.cats, a.Category, KindExpense)
	if c == nil {
		return fail("categoria desconhecida; opções: " + strings.Join(options, ", "))
	}
	if a.Cents != nil && !t.amountHasEvidence(*a.Cents) {
		return fail("o valor do orçamento não aparece na mensagem; confirme com o usuário")
	}
	if err := t.spend(1); err != nil {
		return fail(err.Error())
	}
	if _, err := t.svc.SetBudget(ctx, t.ws.ID, c.ID, a.Cents); err != nil {
		var v *ValidationError
		if errors.As(err, &v) {
			return fail(v.Msg)
		}
		return fail("não foi possível salvar o orçamento")
	}
	if a.Cents == nil {
		return toolResult{OK: true, Reply: "🎯 Orçamento de " + c.Name + " removido."}.String()
	}
	return toolResult{OK: true, Reply: fmt.Sprintf("🎯 Orçamento de %s: %s/mês.", c.Name, FormatBRLShort(*a.Cents))}.String()
}

func (t *turn) merchantHistory(ctx context.Context, args string) string {
	var a struct {
		Merchant string `json:"merchant"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return fail(err.Error())
	}
	hist, err := t.svc.MerchantHistory(ctx, t.ws.ID, a.Merchant)
	if err != nil {
		return fail("não foi possível consultar")
	}
	if len(hist) == 0 {
		return toolResult{OK: true, Details: "nenhum registro anterior com esse nome"}.String()
	}
	return toolResult{OK: true, Details: hist}.String()
}
