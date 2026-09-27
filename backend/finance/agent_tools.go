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

// categoryChoices lists the categories the model may pick: parents by name,
// subcategories as "Parent › Child". A strict enum means the model cannot
// return a category that does not exist.
func categoryChoices(cats []Category) []string {
	byID := CategoryIndex(cats)
	out := make([]string, 0, len(cats))
	for i := range cats {
		out = append(out, CategoryPath(&cats[i], byID))
	}
	return out
}

func categoryField(desc string, choices []string) map[string]any {
	if len(choices) == 0 {
		return sNullString(desc)
	}
	return sEnum(desc, choices, true)
}

func mutationTools(choices []string) []sashabaranov_openai.Tool {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":         sEnum("EXPENSE gasto; INCOME receita; TRANSFER entre contas próprias ou pagamento de fatura do cartão; REFUND estorno de uma compra", TransactionTypes, false),
			"amount_cents": sInt("Valor em centavos exatamente como escrito (R$ 37,90 = 3790; 3 mil = 300000)"),
			"description":  sString("Descrição curta em português (ex: Compra no mercado)"),
			"category": categoryField("A categoria mais específica que dá para afirmar. Se só souber o grupo (ex.: comida), use o grupo (Alimentação). "+
				"null só quando não houver pista nenhuma do que foi", choices),
			"merchant":       sNullString("Estabelecimento ou recebedor, se dito; senão null"),
			"date":           sNullString("Expressão do usuário: hoje, ontem, anteontem, sexta, dia 10, 10/09 ou AAAA-MM-DD; null = hoje"),
			"payer":          sNullString("Nome do membro que pagou; null = quem enviou a mensagem"),
			"payment_method": sEnum("Forma de pagamento se dita; senão null", PaymentMethods, true),
			"confidence":     map[string]any{"type": "number", "description": "Confiança de 0 a 1 na categoria escolhida"},
		},
		"required":             []string{"type", "amount_cents", "description", "category", "merchant", "date", "payer", "payment_method", "confidence"},
		"additionalProperties": false,
	}
	return []sashabaranov_openai.Tool{
		strictTool("create_transaction", "Registra um ou mais gastos, receitas, transferências ou estornos citados na mensagem (máximo 5).",
			map[string]any{"items": map[string]any{"type": "array", "items": item}}),
		strictTool("complete_pending", "Completa o lançamento PENDENTE indicado no contexto com a resposta do usuário (ex.: a categoria que faltava). "+
			"Envie só o que a mensagem trouxe; null no resto.", map[string]any{
			"category":     categoryField("Categoria da lista que corresponde à resposta", choices),
			"amount_cents": sNullInt("Novo valor, só se a mensagem trouxer um valor"),
			"date":         sNullString("Nova data, só se a mensagem trouxer uma"),
			"description":  sNullString("Descrição melhor, se a resposta disser o que foi"),
		}),
		strictTool("update_transaction", "Corrige uma transação do contexto. Envie null nos campos que não mudam.", map[string]any{
			"transaction_id": sInt("Id (#) de uma transação do contexto"),
			"amount_cents":   sNullInt("Novo valor em centavos (null se não mudou)"),
			"category":       categoryField("Nova categoria", choices),
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

// Tool error codes. The model gets the code and the field, so it cannot turn
// "unknown category" into "please confirm the amount"; when no tool succeeds
// the reply is built by the backend from the code (failureReply).
const (
	CodeInvalidArguments   = "INVALID_ARGUMENTS"
	CodeValidation         = "VALIDATION_ERROR"
	CodeCategoryNotFound   = "CATEGORY_NOT_FOUND"
	CodeAmountNotInMessage = "AMOUNT_NOT_IN_MESSAGE"
	CodeInvalidDate        = "INVALID_DATE"
	CodeFutureDate         = "FUTURE_DATE"
	CodeNotInContext       = "TRANSACTION_NOT_IN_CONTEXT"
	CodeNotFound           = "NOT_FOUND"
	CodeMemberNotFound     = "MEMBER_NOT_FOUND"
	CodeMutationLimit      = "MUTATION_LIMIT"
	CodeNoPending          = "NO_PENDING_ACTION"
	CodeInvalidPeriod      = "INVALID_PERIOD"
	CodeInternal           = "INTERNAL_ERROR"
	CodeUnknownTool        = "UNKNOWN_TOOL"
	CodeNothingToUndo      = "NOTHING_TO_UNDO"
)

// turn holds the state of one message being processed: who wrote it, what
// the tools may touch, and what they touched.
type turn struct {
	svc          *Service
	ws           *Workspace
	actor        Actor
	sender       *Member
	chatJID      string
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

	// pending is the open question this message may answer; pendingOpened is
	// set when this turn asks one (its reply id is recorded as the question).
	pending         *PendingAction
	pendingOpened   *int64
	pendingAwaiting string
	pendingState    string

	failures    []toolFailure
	successes   int
	internalErr error

	// For the structured log (never message text).
	path       string
	intents    []string
	confidence float64
}

type toolFailure struct {
	Tool, Code, Field, Message string
}

type toolResult struct {
	OK        bool   `json:"ok"`
	Reply     string `json:"reply,omitempty"`
	Code      string `json:"code,omitempty"`
	Field     string `json:"field,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Message   string `json:"message,omitempty"`
	Details   any    `json:"details,omitempty"`
}

func (r toolResult) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

func failCode(code, field, msg string) string {
	return toolResult{OK: false, Code: code, Field: field, Message: msg}.String()
}

func fail(msg string) string { return failCode(CodeValidation, "", msg) }

// internal records an infrastructure error: the turn is retried by the
// inbox instead of answering with a made-up explanation.
func (t *turn) internal(err error) string {
	if t.internalErr == nil {
		t.internalErr = err
	}
	return toolResult{OK: false, Code: CodeInternal, Retryable: true, Message: "falha temporária do sistema"}.String()
}

// serviceError maps a service error to a tool error.
func (t *turn) serviceError(err error, field string) string {
	var v *ValidationError
	switch {
	case errors.As(err, &v):
		return failCode(CodeValidation, field, v.Msg)
	case errors.Is(err, ErrNotFound):
		return failCode(CodeNotFound, "transaction_id", "transação não encontrada")
	}
	return t.internal(err)
}

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
	t.intents = append(t.intents, name)
	out := t.dispatch(ctx, name, args)
	var r toolResult
	if json.Unmarshal([]byte(out), &r) == nil {
		if r.OK {
			t.successes++
		} else {
			t.failures = append(t.failures, toolFailure{Tool: name, Code: r.Code, Field: r.Field, Message: r.Message})
		}
	}
	return out
}

func (t *turn) dispatch(ctx context.Context, name, args string) string {
	switch name {
	case "create_transaction":
		return t.createTransaction(ctx, args)
	case "complete_pending":
		return t.completePending(ctx, args)
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
	return failCode(CodeUnknownTool, "", "ferramenta desconhecida")
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

// confirmation renders the short line sent after a change:
// "✅ R$ 33,00 · Alimentação · Cookies'N Blues · hoje".
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
	if tx.Merchant != nil && *tx.Merchant != "" && normalize(*tx.Merchant) != normalize(t.categoryLabel(tx.CategoryID)) {
		parts = append(parts, *tx.Merchant)
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

// amountHasEvidence is the anti-hallucination check for a NEW amount: it
// must be written in the message, or match the amount read from the receipt.
func (t *turn) amountHasEvidence(cents int64) bool {
	if t.receipt != nil && t.receipt.AmountCents != nil && *t.receipt.AmountCents == cents {
		return true
	}
	return AmountInText(cents, t.text)
}

// createTransaction registers what the message says. The backend decides:
// an explicit amount is never asked again; the only questions are about
// what is really missing (category, date) or really doubtful (an amount
// that is not in the message, a probable duplicate).
func (t *turn) createTransaction(ctx context.Context, args string) string {
	var a struct {
		Items []createItem `json:"items"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	if len(a.Items) == 0 {
		return failCode(CodeInvalidArguments, "items", "nenhum item informado")
	}
	if err := t.spend(len(a.Items)); err != nil {
		return failCode(CodeMutationLimit, "items", err.Error())
	}

	var inputs []TxInput
	var suggestions []*int64
	for _, it := range a.Items {
		if it.Confidence > t.confidence {
			t.confidence = it.Confidence
		}
		if !contains(TransactionTypes, it.Type) {
			return failCode(CodeInvalidArguments, "type", "tipo inválido")
		}
		in := TxInput{
			Type: it.Type, AmountCents: it.AmountCents, Description: it.Description, Merchant: deref(it.Merchant),
			PaymentMethod: deref(it.PaymentMethod), Source: t.source, SourceItem: t.items, AttachmentID: t.attachmentID,
			AIConfidence: ptr(it.Confidence), PossibleDuplicateOf: t.duplicateOf,
		}
		t.items++
		if t.receipt != nil && t.receipt.ExternalRef != nil {
			in.ExternalRef = *t.receipt.ExternalRef
		}

		// Category: the model's choice when it is confident; otherwise a
		// suggestion for the question. A merchant already filed before fills
		// the gap from history (the user's own past choice wins over a guess).
		var suggestion *int64
		if in.Type != TypeTransfer {
			if it.Category != nil && strings.TrimSpace(*it.Category) != "" {
				c, options := t.resolveCategory(*it.Category, in.Type)
				if c == nil {
					return failCode(CodeCategoryNotFound, "category", "categoria inexistente; use uma destas: "+strings.Join(options, ", "))
				}
				if it.Confidence >= t.ws.ConfidenceThreshold {
					in.CategoryID = &c.ID
				} else {
					suggestion = &c.ID
				}
			}
			if in.CategoryID == nil && in.Merchant != "" {
				if id, err := t.svc.MerchantCategory(ctx, t.ws.ID, in.Merchant, kindOf(in.Type)); err != nil {
					return t.internal(err)
				} else if id != nil {
					in.CategoryID, suggestion = id, nil
				}
			}
			if in.CategoryID == nil {
				in.PendingReasons = append(in.PendingReasons, ReasonCategoryMissing)
			}
		} else if it.Confidence < t.ws.ConfidenceThreshold {
			in.PendingReasons = append(in.PendingReasons, ReasonLowConfidence)
		}

		date, err := ResolveDate(deref(it.Date), t.svc.now())
		switch {
		case errors.Is(err, ErrFutureDate):
			return failCode(CodeFutureDate, "date", "a data informada está no futuro")
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

		if !t.amountHasEvidence(in.AmountCents) {
			in.PendingReasons = append(in.PendingReasons, ReasonAmountUnchecked)
		}
		if t.duplicateOf != nil {
			in.PendingReasons = append(in.PendingReasons, ReasonDuplicate)
		}
		inputs = append(inputs, in)
		suggestions = append(suggestions, suggestion)
	}

	txs, err := t.svc.CreateTransactions(ctx, t.ws.ID, t.actor, inputs)
	if err != nil {
		return t.serviceError(err, "")
	}

	var lines []string
	var question string
	var details []map[string]any
	for i := range txs {
		tx := &txs[i]
		t.allow(tx.ID)
		t.touched = append(t.touched, tx.ID)
		details = append(details, map[string]any{"id": tx.ID, "status": tx.Status})
		if tx.Status == StatusConfirmed {
			lines = append(lines, t.confirmation(tx))
			continue
		}
		awaiting := awaitingFor(reasonsOf(tx))
		line, q := t.pendingMessage(tx, awaiting, suggestions[i])
		lines = append(lines, line)
		// One open question per member: the first pending item gets it;
		// others stay pending in the panel.
		if question == "" {
			act, err := t.svc.OpenPending(ctx, t.ws.ID, t.actor.MemberID, t.chatJID, t.actor.InboxID, tx.ID, awaiting, suggestions[i])
			if err != nil {
				return t.internal(err)
			}
			t.pendingOpened, t.pendingAwaiting = &act.ID, awaiting
			question = q
		}
	}
	reply := strings.Join(lines, "\n")
	if question != "" {
		reply += "\n" + question
	}
	return toolResult{OK: true, Reply: reply, Details: details}.String()
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

// patchFromAnswer validates the fields of a correction. Only a CHANGED
// amount needs evidence in the message: restating the current value (models
// do that) is not a change.
func (t *turn) patchFromAnswer(ctx context.Context, current *Transaction, txType string, amount *int64, category, date *string) (TxPatch, string) {
	var p TxPatch
	if amount != nil && *amount != current.AmountCents {
		if !t.amountHasEvidence(*amount) {
			return p, failCode(CodeAmountNotInMessage, "amount_cents", "o novo valor não está escrito na mensagem")
		}
		p.AmountCents = amount
	}
	if category != nil && strings.TrimSpace(*category) != "" && txType != TypeTransfer {
		c, options := t.resolveCategory(*category, txType)
		if c == nil {
			return p, failCode(CodeCategoryNotFound, "category", "categoria inexistente; use uma destas: "+strings.Join(options, ", "))
		}
		p.CategoryID = &c.ID
	}
	if date != nil && strings.TrimSpace(*date) != "" {
		d, err := ResolveDate(*date, t.svc.now())
		switch {
		case errors.Is(err, ErrFutureDate):
			return p, failCode(CodeFutureDate, "date", "a data está no futuro")
		case err != nil:
			return p, failCode(CodeInvalidDate, "date", "data não reconhecida")
		}
		p.Date = &d
	}
	return p, ""
}

func (t *turn) updateTransaction(ctx context.Context, args string) string {
	var a updateArgs
	if err := decodeArgs(args, &a); err != nil {
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	if !t.allowed[a.TransactionID] {
		return failCode(CodeNotInContext, "transaction_id", "essa transação não está no contexto desta conversa")
	}
	current, err := t.svc.GetTransaction(ctx, t.ws.ID, a.TransactionID, false)
	if err != nil {
		return t.serviceError(err, "transaction_id")
	}
	if err := t.spend(1); err != nil {
		return failCode(CodeMutationLimit, "", err.Error())
	}

	txType := current.Type
	if a.Type != nil {
		if !contains(TransactionTypes, *a.Type) {
			return failCode(CodeInvalidArguments, "type", "tipo inválido")
		}
		txType = *a.Type
	}
	patch, bad := t.patchFromAnswer(ctx, current, txType, a.AmountCents, a.Category, a.Date)
	if bad != "" {
		return bad
	}
	if a.Type != nil && *a.Type != current.Type {
		patch.Type = a.Type
	}
	patch.Confirm, patch.Description, patch.Merchant = a.Confirm, a.Description, a.Merchant
	if a.Payer != nil {
		p, err := t.svc.ResolvePayer(ctx, t.ws.ID, t.actor.MemberID, *a.Payer)
		if err != nil {
			return failCode(CodeMemberNotFound, "payer", err.Error())
		}
		patch.PayerMemberID = p
	}

	tx, changed, err := t.svc.UpdateTransaction(ctx, t.ws.ID, a.TransactionID, t.actor, patch)
	if err != nil {
		return t.serviceError(err, "")
	}
	t.touched = append(t.touched, tx.ID)
	if err := t.svc.SyncPendingForTx(ctx, t.ws.ID, tx); err != nil {
		return t.internal(err)
	}
	if !changed {
		return toolResult{OK: true, Reply: "Nada mudou: " + strings.TrimPrefix(t.confirmation(tx), "✅ ")}.String()
	}
	if tx.Status == StatusConfirmed {
		if current.Status == StatusConfirmed {
			return toolResult{OK: true, Reply: "✏️ Corrigido: " + strings.TrimPrefix(strings.TrimPrefix(t.confirmation(tx), "✅ "), "💰 ")}.String()
		}
		return toolResult{OK: true, Reply: t.confirmation(tx)}.String()
	}
	line, question := t.pendingMessage(tx, awaitingFor(reasonsOf(tx)), nil)
	return toolResult{OK: true, Reply: line + "\n" + question}.String()
}

// completePending answers the open question with what the message said
// ("Cookies" -> Alimentação). The draft comes from the database; nothing is
// rebuilt from the chat.
func (t *turn) completePending(ctx context.Context, args string) string {
	var a struct {
		Category    *string `json:"category"`
		AmountCents *int64  `json:"amount_cents"`
		Date        *string `json:"date"`
		Description *string `json:"description"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	if t.pending == nil {
		return failCode(CodeNoPending, "", "não há lançamento pendente aguardando resposta")
	}
	current, err := t.svc.GetTransaction(ctx, t.ws.ID, t.pending.TransactionID, false)
	if err != nil {
		return t.serviceError(err, "")
	}
	if err := t.spend(1); err != nil {
		return failCode(CodeMutationLimit, "", err.Error())
	}
	patch, bad := t.patchFromAnswer(ctx, current, current.Type, a.AmountCents, a.Category, a.Date)
	if bad != "" {
		return bad
	}
	if a.Description != nil && strings.TrimSpace(*a.Description) != "" {
		patch.Description = a.Description
	}
	if t.pending.Awaiting == AwaitCategory && patch.CategoryID == nil && current.Type != TypeTransfer {
		return failCode(CodeCategoryNotFound, "category", "a resposta não indica uma categoria da lista")
	}
	reply, err := t.applyPending(ctx, t.pending, patch)
	if err != nil {
		return t.serviceError(err, "")
	}
	return toolResult{OK: true, Reply: reply}.String()
}

func (t *turn) deleteTransaction(ctx context.Context, args string) string {
	var a struct {
		TransactionID int64 `json:"transaction_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	if !t.allowed[a.TransactionID] {
		return failCode(CodeNotInContext, "transaction_id", "essa transação não está no contexto desta conversa")
	}
	if err := t.spend(1); err != nil {
		return failCode(CodeMutationLimit, "", err.Error())
	}
	tx, err := t.svc.DeleteTransaction(ctx, t.ws.ID, a.TransactionID, t.actor)
	if err != nil {
		return t.serviceError(err, "transaction_id")
	}
	if err := t.svc.SyncPendingForTx(ctx, t.ws.ID, tx); err != nil {
		return t.internal(err)
	}
	return toolResult{OK: true, Reply: fmt.Sprintf("🗑️ Apagado: %s · %s · %s. (diga \"desfaz\" para voltar)",
		FormatBRL(tx.AmountCents), t.categoryLabel(tx.CategoryID), HumanDate(tx.TransactionDate, t.svc.now()))}.String()
}

func (t *turn) undo(ctx context.Context, args string) string {
	var a struct{}
	if err := decodeArgs(args, &a); err != nil {
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	if err := t.spend(1); err != nil {
		return failCode(CodeMutationLimit, "", err.Error())
	}
	r, err := t.svc.UndoLast(ctx, t.ws.ID, t.actor)
	switch {
	case errors.Is(err, ErrNothingToUndo):
		return toolResult{OK: true, Reply: "Não encontrei nada recente seu para desfazer."}.String()
	case errors.Is(err, ErrUndoStale):
		return toolResult{OK: true, Reply: "Não dá para desfazer: esse registro foi alterado depois por outra pessoa."}.String()
	case err != nil:
		return t.internal(err)
	}
	tx := r.Transaction
	t.touched = append(t.touched, tx.ID)
	if err := t.svc.SyncPendingForTx(ctx, t.ws.ID, tx); err != nil {
		return t.internal(err)
	}
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
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	c, options := resolveCategory(t.cats, a.Category, KindExpense)
	if c == nil {
		return failCode(CodeCategoryNotFound, "category", "categoria inexistente; use uma destas: "+strings.Join(options, ", "))
	}
	if a.Cents != nil && !t.amountHasEvidence(*a.Cents) {
		return failCode(CodeAmountNotInMessage, "monthly_budget_cents", "o valor do orçamento não está escrito na mensagem")
	}
	if err := t.spend(1); err != nil {
		return failCode(CodeMutationLimit, "", err.Error())
	}
	if _, err := t.svc.SetBudget(ctx, t.ws.ID, c.ID, a.Cents); err != nil {
		return t.serviceError(err, "monthly_budget_cents")
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
		return failCode(CodeInvalidArguments, "", err.Error())
	}
	hist, err := t.svc.MerchantHistory(ctx, t.ws.ID, a.Merchant)
	if err != nil {
		return t.internal(err)
	}
	if len(hist) == 0 {
		return toolResult{OK: true, Details: "nenhum registro anterior com esse nome"}.String()
	}
	return toolResult{OK: true, Details: hist}.String()
}

// failureReply is the answer when every tool of the turn failed. It is built
// from the error code, so the user never reads an invented cause.
func (t *turn) failureReply(f toolFailure) string {
	switch f.Code {
	case CodeCategoryNotFound:
		return "Não encontrei essa categoria. Foi com o quê? (ex.: Alimentação, Mercado, Transporte, Lazer)"
	case CodeNotInContext, CodeNotFound:
		return "Não sei qual lançamento alterar. Responda à mensagem de confirmação dele."
	case CodeAmountNotInMessage:
		return "Qual é o valor certo? Escreva o número, por exemplo 35,90."
	case CodeInvalidDate, CodeFutureDate:
		return "Qual foi o dia? Ex.: hoje, ontem, 10/09."
	case CodeMutationLimit:
		return "São muitas alterações de uma vez. Mande uma por mensagem."
	case CodeNoPending:
		return "Não encontrei uma confirmação pendente. Me diga novamente o gasto."
	case CodeMemberNotFound:
		return "Não sei quem é essa pessoa. " + f.Message
	case CodeInvalidPeriod:
		return "Não entendi o período. Ex.: “este mês”, “mês passado”, “de 10 a 20”."
	case CodeValidation:
		if f.Message == "" {
			break
		}
		switch f.Tool {
		case "financial_summary", "list_transactions", "compare_periods", "budget_status", "insights":
			return "Não consegui consultar: " + f.Message
		}
		return "Não consegui registrar: " + f.Message
	}
	return "Não entendi. Pode reformular?"
}
