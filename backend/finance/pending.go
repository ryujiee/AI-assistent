package finance

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Pending actions hold the conversation state of an entry that is not
// confirmed yet. The draft is the PENDING transaction; the action records
// which question was asked and what answer is expected, so replies such as
// "sim", "mercado", "ontem" or "na verdade foi 35" are resolved by the backend
// against a known draft, not reconstructed by the model from the chat.

// What the last question expects.
const (
	AwaitCategory  = "CATEGORY"
	AwaitDate      = "DATE"
	AwaitConfirm   = "CONFIRM"
	AwaitDuplicate = "CONFIRM_DUPLICATE"
)

const (
	pendingOpen       = "OPEN"
	pendingExecuted   = "EXECUTED"
	pendingCancelled  = "CANCELLED"
	pendingExpired    = "EXPIRED"
	pendingSuperseded = "SUPERSEDED"

	// PendingTTL is how long a question can still be answered.
	PendingTTL = 2 * time.Hour
	// maxPendingAttempts ends a conversation that keeps failing to resolve,
	// instead of asking the same thing forever.
	maxPendingAttempts = 3
	// recentExecution is how long a stray "sim" is read as "it's already done".
	recentExecution = 10 * time.Minute
)

type PendingAction struct {
	ID                  int64
	WorkspaceID         int64
	MemberID            *int64
	TransactionID       int64
	Awaiting            string
	SuggestedCategoryID *int64
	Status              string
	QuestionMessageID   *string
	Attempts            int
}

const pendingCols = "id, workspace_id, member_id, transaction_id, awaiting, suggested_category_id, status, question_message_id, attempts"

func scanPending(row pgx.Row) (*PendingAction, error) {
	var p PendingAction
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.MemberID, &p.TransactionID, &p.Awaiting, &p.SuggestedCategoryID, &p.Status, &p.QuestionMessageID, &p.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

// awaitingFor picks the single question that unblocks a pending entry, in
// order: duplicate, amount, category, date.
func awaitingFor(reasons []string) string {
	switch {
	case contains(reasons, ReasonDuplicate):
		return AwaitDuplicate
	case contains(reasons, ReasonAmountUnchecked), contains(reasons, ReasonLowConfidence):
		return AwaitConfirm
	case contains(reasons, ReasonCategoryMissing):
		return AwaitCategory
	case contains(reasons, ReasonDateUnclear):
		return AwaitDate
	}
	return AwaitConfirm
}

func reasonsOf(tx *Transaction) []string {
	if tx.PendingReason == nil {
		return nil
	}
	var out []string
	for _, r := range strings.Split(*tx.PendingReason, ",") {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

// OpenPending records the question asked about a pending transaction. A
// member has at most one open question: an older one is superseded (its
// transaction stays PENDING in the panel).
func (s *Service) OpenPending(ctx context.Context, wsID int64, memberID *int64, chatJID string, inboxID *int64, txID int64, awaiting string, suggested *int64) (*PendingAction, error) {
	var out *PendingAction
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE finance_pending_actions SET status = 'SUPERSEDED', resolved_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND member_id IS NOT DISTINCT FROM $2 AND status = 'OPEN'`, wsID, memberID); err != nil {
			return err
		}
		var err error
		out, err = scanPending(tx.QueryRow(ctx, `
			INSERT INTO finance_pending_actions (workspace_id, member_id, chat_jid, source_inbox_id, transaction_id, awaiting, suggested_category_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now() + $8::interval)
			RETURNING `+pendingCols, wsID, memberID, chatJID, inboxID, txID, awaiting, suggested, PendingTTL.String()))
		return err
	})
	return out, err
}

// ActivePending finds the open question a message answers: the one it quotes,
// otherwise the sender's question asked in the last unquotedAnswerWindow (an
// unrelated message an hour later is not an answer). Expired questions are
// closed on the way.
func (s *Service) ActivePending(ctx context.Context, wsID int64, memberID *int64, quotedID string) (*PendingAction, error) {
	if _, err := s.DB.Exec(ctx, `UPDATE finance_pending_actions SET status = 'EXPIRED', resolved_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND status = 'OPEN' AND expires_at < now()`, wsID); err != nil {
		return nil, err
	}
	if quotedID != "" {
		p, err := scanPending(s.DB.QueryRow(ctx, "SELECT "+pendingCols+` FROM finance_pending_actions
			WHERE workspace_id = $1 AND question_message_id = $2 AND status = 'OPEN'`, wsID, quotedID))
		if !errors.Is(err, ErrNotFound) {
			return p, err
		}
	}
	if memberID == nil {
		return nil, ErrNotFound
	}
	return scanPending(s.DB.QueryRow(ctx, "SELECT "+pendingCols+` FROM finance_pending_actions
		WHERE workspace_id = $1 AND member_id = $2 AND status = 'OPEN' AND updated_at > now() - $3::interval
		ORDER BY id DESC LIMIT 1`, wsID, *memberID, unquotedAnswerWindow))
}

// unquotedAnswerWindow: how long a reply without a quote still counts as the
// answer to the sender's open question.
const unquotedAnswerWindow = "10 minutes"

func (s *Service) resolvePending(ctx context.Context, id int64, status string) error {
	_, err := s.DB.Exec(ctx, `UPDATE finance_pending_actions SET status = $2, resolved_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'OPEN'`, id, status)
	return err
}

// reaskPending changes the expected answer (or keeps it) and counts one more
// unresolved round; it returns the new attempt count.
func (s *Service) reaskPending(ctx context.Context, id int64, awaiting string, suggested *int64, countAttempt bool) (int, error) {
	inc := 0
	if countAttempt {
		inc = 1
	}
	var attempts int
	err := s.DB.QueryRow(ctx, `UPDATE finance_pending_actions SET awaiting = $2, suggested_category_id = $3,
			attempts = attempts + $4, expires_at = now() + $5::interval, updated_at = now()
		WHERE id = $1 RETURNING attempts`, id, awaiting, suggested, inc, PendingTTL.String()).Scan(&attempts)
	return attempts, err
}

// SetPendingQuestion remembers which bot message asked the question, so a
// quoted reply finds the right action.
func (s *Service) SetPendingQuestion(ctx context.Context, id int64, messageID string) error {
	_, err := s.DB.Exec(ctx, "UPDATE finance_pending_actions SET question_message_id = $2, updated_at = now() WHERE id = $1", id, messageID)
	return err
}

// SyncPendingForTx keeps open questions consistent after a transaction
// changed by any path (a tool, the panel): confirmed -> executed, deleted ->
// cancelled, still pending -> the next question.
func (s *Service) SyncPendingForTx(ctx context.Context, wsID int64, tx *Transaction) error {
	switch {
	case tx.DeletedAt != nil:
		_, err := s.DB.Exec(ctx, `UPDATE finance_pending_actions SET status = 'CANCELLED', resolved_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND transaction_id = $2 AND status = 'OPEN'`, wsID, tx.ID)
		return err
	case tx.Status == StatusConfirmed:
		_, err := s.DB.Exec(ctx, `UPDATE finance_pending_actions SET status = 'EXECUTED', resolved_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND transaction_id = $2 AND status = 'OPEN'`, wsID, tx.ID)
		return err
	}
	_, err := s.DB.Exec(ctx, `UPDATE finance_pending_actions SET awaiting = $3, updated_at = now()
		WHERE workspace_id = $1 AND transaction_id = $2 AND status = 'OPEN'`, wsID, tx.ID, awaitingFor(reasonsOf(tx)))
	return err
}

// recentlyExecuted returns the transaction the member confirmed in the last
// few minutes, if any (a late "sim" is then answered with "already done").
func (s *Service) recentlyExecuted(ctx context.Context, wsID int64, memberID *int64) *Transaction {
	if memberID == nil {
		return nil
	}
	var txID int64
	err := s.DB.QueryRow(ctx, `SELECT transaction_id FROM finance_pending_actions
		WHERE workspace_id = $1 AND member_id = $2 AND status = 'EXECUTED' AND resolved_at > now() - $3::interval
		ORDER BY resolved_at DESC LIMIT 1`, wsID, *memberID, recentExecution.String()).Scan(&txID)
	if err != nil {
		return nil
	}
	tx, err := s.GetTransaction(ctx, wsID, txID, false)
	if err != nil || tx.Status != StatusConfirmed {
		return nil
	}
	return tx
}

// ---- reply classification (deterministic) ----

type replyKind int

const (
	replyOther replyKind = iota
	replyAffirmStrong
	replyAffirmWeak
	replyNegative
)

var (
	replyWord   = regexp.MustCompile(`[\p{L}\p{N}]+`)
	affirmWords = map[string]bool{"sim": true, "s": true, "isso": true, "mesmo": true, "ai": true, "pode": true, "registrar": true,
		"registra": true, "registre": true, "lancar": true, "lanca": true, "confirmo": true, "confirma": true, "confirmado": true,
		"confirmar": true, "correto": true, "certo": true, "ta": true, "esta": true, "e": true, "exato": true, "exatamente": true,
		"positivo": true, "claro": true, "com": true, "certeza": true, "yes": true, "isto": true, "ok": true, "okay": true,
		"blz": true, "beleza": true, "fechado": true, "perfeito": true, "show": true, "vai": true, "manda": true, "bala": true}
	// weakAffirm words confirm an open question but are also plain chat
	// acknowledgements ("ok" after a confirmation must not trigger anything).
	weakAffirm   = map[string]bool{"ok": true, "okay": true, "blz": true, "beleza": true, "fechado": true, "perfeito": true, "show": true}
	negativeCore = map[string]bool{"nao": true, "n": true, "cancela": true, "cancelar": true, "cancele": true, "esquece": true,
		"esqueca": true, "deixa": true, "deixe": true, "errado": true, "nope": true}
	negativeExtra = map[string]bool{"pra": true, "para": true, "la": true, "nada": true, "isso": true, "registra": true, "registrar": true,
		"precisa": true, "obrigado": true, "obrigada": true}
	fillerPrefix = regexp.MustCompile(`^(foi|era|e|eh|em|no|na|nos|nas|de|do|da|com|categoria|coloca|coloque|bota|poe|pode|por|pra|para|o|a)\s+`)
)

// classifyReply reads short confirmations and refusals. Anything carrying
// more information (an amount, a category, a sentence) is replyOther.
func classifyReply(text string) replyKind {
	raw := strings.TrimSpace(text)
	for _, e := range []string{"👍", "✅", "👌", "🙌"} {
		raw = strings.ReplaceAll(raw, e, " ok ")
	}
	for _, e := range []string{"👎", "❌", "🚫"} {
		raw = strings.ReplaceAll(raw, e, " nao ")
	}
	words := replyWord.FindAllString(normalize(raw), -1)
	if len(words) == 0 || len(words) > 6 {
		return replyOther
	}
	negative, strong, weak := false, false, false
	for _, w := range words {
		switch {
		case negativeCore[w]:
			negative = true
		case affirmWords[w]:
			if weakAffirm[w] {
				weak = true
			} else if w != "e" && w != "com" && w != "mesmo" && w != "ai" && w != "ta" && w != "esta" {
				strong = true
			}
		case negativeExtra[w]:
		default:
			return replyOther
		}
	}
	switch {
	case negative:
		return replyNegative
	case strong:
		return replyAffirmStrong
	case weak:
		return replyAffirmWeak
	}
	return replyOther
}

var correctionPrefix = regexp.MustCompile(`^(na verdade|foi|era|e|eh|sao|o valor|valor|corrige|corrigindo|ops|opa)\b`)

// amountCorrection reads "na verdade foi 35", "foi 38", or just "35" while a
// confirmation is open. The amount is written by the user, so it counts as
// evidence and is not asked again.
func amountCorrection(text, awaiting string) (int64, bool) {
	amounts := ExtractAmounts(text)
	if len(amounts) == 0 {
		return 0, false
	}
	n := normalize(text)
	bare := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(n, "r$"), "reais"), "real"))
	// Only an explicit correction ("foi 38") or a bare amount ("38") counts:
	// "paguei 80 de luz" is a new expense, even while a confirmation is open.
	if correctionPrefix.MatchString(n) {
		return amounts[0], true
	}
	if v, err := ParseAmount(bare); err == nil && v == amounts[0] {
		return v, true
	}
	return 0, false
}

// categoryFromAnswer maps a short answer ("mercado", "foi no restaurante")
// to an existing category, without guessing.
func categoryFromAnswer(cats []Category, text, kind string) *Category {
	n := strings.Trim(normalize(text), " .!?,")
	for i := 0; i < 3; i++ {
		trimmed := fillerPrefix.ReplaceAllString(n, "")
		if trimmed == n {
			break
		}
		n = trimmed
	}
	if n == "" || len(strings.Fields(n)) > 4 {
		return nil
	}
	c, _ := resolveCategory(cats, n, kind)
	return c
}

func kindOf(txType string) string {
	if txType == TypeIncome {
		return KindIncome
	}
	return KindExpense
}

// ---- conversation handling ----

// pendingMessage renders a pending entry and the one question that unblocks it.
func (t *turn) pendingMessage(tx *Transaction, awaiting string, suggested *int64) (string, string) {
	amount := FormatBRL(tx.AmountCents)
	switch awaiting {
	case AwaitDuplicate:
		if tx.PossibleDuplicateOf != nil {
			if prev, err := t.svc.GetTransaction(context.Background(), t.ws.ID, *tx.PossibleDuplicateOf, true); err == nil {
				return "⚠️ Esse comprovante parece já ter sido registrado como " + FormatBRL(prev.AmountCents) + " em " +
					t.categoryLabel(prev.CategoryID) + " (" + HumanDate(prev.TransactionDate, t.svc.now()) + ").", "Deseja registrar novamente?"
			}
		}
		return "⚠️ Esse comprovante parece já ter sido registrado.", "Deseja registrar novamente?"
	case AwaitConfirm:
		question := "Confirma?"
		if contains(reasonsOf(tx), ReasonAmountUnchecked) {
			question = "O valor está certo?"
		}
		what := t.categoryLabel(tx.CategoryID)
		if tx.Type == TypeTransfer {
			what = "transferência"
		}
		return "💬 Entendi " + amount + " · " + what + " · " + HumanDate(tx.TransactionDate, t.svc.now()) + ".", question
	case AwaitDate:
		return "💬 " + amount + " · " + t.categoryLabel(tx.CategoryID) + " anotado.", "Qual foi o dia?"
	}
	line := "💬 " + amount + " anotado."
	if t.receipt != nil {
		line = "🧾 Identifiquei " + amount + ", mas não consegui saber a categoria."
	}
	if suggested != nil {
		if c, ok := t.byCat[*suggested]; ok {
			return line, "Foi " + c.Name + " mesmo? Se não, me diga a categoria."
		}
	}
	switch {
	case tx.Type == TypeIncome:
		return line, "Foi de quê? (ex.: Salário, Renda extra)"
	case t.receipt != nil:
		return line, "Foi com o quê? (ex.: Mercado, Restaurantes, Outros)"
	}
	return line, "Foi com o quê?"
}

// applyPending completes the draft with what the user said and confirms it.
// If something else is still missing, the next single question is asked.
func (t *turn) applyPending(ctx context.Context, act *PendingAction, patch TxPatch) (string, error) {
	patch.Confirm = true
	tx, _, err := t.svc.UpdateTransaction(ctx, t.ws.ID, act.TransactionID, t.actor, patch)
	if err != nil {
		return "", err
	}
	t.touched = append(t.touched, tx.ID)
	t.allow(tx.ID)
	if tx.Status == StatusConfirmed {
		if err := t.svc.resolvePending(ctx, act.ID, pendingExecuted); err != nil {
			return "", err
		}
		t.pendingState = pendingExecuted
		return t.confirmation(tx), nil
	}
	awaiting := awaitingFor(reasonsOf(tx))
	if _, err := t.svc.reaskPending(ctx, act.ID, awaiting, nil, false); err != nil {
		return "", err
	}
	t.pendingOpened, t.pendingAwaiting = &act.ID, awaiting
	line, question := t.pendingMessage(tx, awaiting, nil)
	return line + "\n" + question, nil
}

// reask repeats (or rephrases) the open question, giving up after a few
// unresolved rounds so the conversation never loops.
func (t *turn) reask(ctx context.Context, act *PendingAction, tx *Transaction, awaiting string, suggested *int64, prefix string) (string, error) {
	attempts, err := t.svc.reaskPending(ctx, act.ID, awaiting, suggested, true)
	if err != nil {
		return "", err
	}
	if attempts >= maxPendingAttempts {
		if err := t.svc.resolvePending(ctx, act.ID, pendingExpired); err != nil {
			return "", err
		}
		t.pendingState = pendingExpired
		return "Tudo bem, deixei o lançamento de " + FormatBRL(tx.AmountCents) + " como pendente no painel para completar depois.", nil
	}
	t.pendingOpened, t.pendingAwaiting = &act.ID, awaiting
	_, question := t.pendingMessage(tx, awaiting, suggested)
	if prefix != "" {
		return prefix + " " + question, nil
	}
	return question, nil
}

// replyToPending resolves a reply against the open question without the
// model: confirmations, refusals, amount corrections, a known category or a
// date. handled=false leaves the message to the model (with the pending
// draft in its context).
func (t *turn) replyToPending(ctx context.Context) (string, bool, error) {
	kind := classifyReply(t.text)
	act := t.pending
	var tx *Transaction
	if act != nil {
		var err error
		tx, err = t.svc.GetTransaction(ctx, t.ws.ID, act.TransactionID, true)
		switch {
		case err != nil && !errors.Is(err, ErrNotFound):
			return "", false, err
		case err != nil || tx.DeletedAt != nil:
			if err := t.svc.resolvePending(ctx, act.ID, pendingCancelled); err != nil {
				return "", false, err
			}
			act, t.pending = nil, nil
		case tx.Status == StatusConfirmed:
			if err := t.svc.resolvePending(ctx, act.ID, pendingExecuted); err != nil {
				return "", false, err
			}
			act, t.pending = nil, nil
		}
	}

	if act == nil {
		switch kind {
		case replyAffirmStrong:
			t.path = "deterministic"
			if done := t.svc.recentlyExecuted(ctx, t.ws.ID, t.actor.MemberID); done != nil {
				return "Já está registrado: " + strings.TrimPrefix(t.confirmation(done), "✅ "), true, nil
			}
			return "Não encontrei uma confirmação pendente. Me diga novamente o gasto.", true, nil
		case replyAffirmWeak, replyNegative:
			t.path = "deterministic"
			return "", true, nil // plain acknowledgement: stay quiet
		}
		return "", false, nil
	}

	t.path = "deterministic"
	t.allow(tx.ID)
	if cents, ok := amountCorrection(t.text, act.Awaiting); ok {
		reply, err := t.applyPending(ctx, act, TxPatch{AmountCents: &cents})
		return t.pendingResult(ctx, act, tx, reply, err)
	}

	switch kind {
	case replyNegative:
		if act.Awaiting == AwaitCategory && act.SuggestedCategoryID != nil {
			reply, err := t.reask(ctx, act, tx, AwaitCategory, nil, "Tudo bem.")
			return reply, true, err
		}
		if _, err := t.svc.DeleteTransaction(ctx, t.ws.ID, tx.ID, t.actor); err != nil {
			return "", false, err
		}
		if err := t.svc.resolvePending(ctx, act.ID, pendingCancelled); err != nil {
			return "", false, err
		}
		t.pendingState = pendingCancelled
		return "Beleza, não registrei.", true, nil

	case replyAffirmStrong, replyAffirmWeak:
		switch act.Awaiting {
		case AwaitConfirm, AwaitDuplicate:
			reply, err := t.applyPending(ctx, act, TxPatch{})
			return t.pendingResult(ctx, act, tx, reply, err)
		case AwaitCategory:
			if act.SuggestedCategoryID != nil {
				reply, err := t.applyPending(ctx, act, TxPatch{CategoryID: act.SuggestedCategoryID})
				return t.pendingResult(ctx, act, tx, reply, err)
			}
		}
		reply, err := t.reask(ctx, act, tx, act.Awaiting, act.SuggestedCategoryID, "Preciso só de mais uma informação:")
		return reply, true, err
	}

	switch act.Awaiting {
	case AwaitCategory:
		if c := categoryFromAnswer(t.cats, t.text, kindOf(tx.Type)); c != nil {
			reply, err := t.applyPending(ctx, act, TxPatch{CategoryID: &c.ID})
			return t.pendingResult(ctx, act, tx, reply, err)
		}
	case AwaitDate:
		d, err := ResolveDate(strings.TrimSpace(fillerPrefix.ReplaceAllString(normalize(t.text), "")), t.svc.now())
		if err == nil {
			reply, err := t.applyPending(ctx, act, TxPatch{Date: &d})
			return t.pendingResult(ctx, act, tx, reply, err)
		}
		if errors.Is(err, ErrFutureDate) {
			reply, err := t.reask(ctx, act, tx, AwaitDate, nil, "Essa data ainda não chegou.")
			return reply, true, err
		}
	}
	// Free text ("Cookies"): the model interprets it with the draft in context.
	t.path = "llm"
	return "", false, nil
}

// pendingResult maps the outcome of applying an answer: validation problems
// re-ask with the reason, infrastructure errors go back to the inbox retry.
func (t *turn) pendingResult(ctx context.Context, act *PendingAction, tx *Transaction, reply string, err error) (string, bool, error) {
	var v *ValidationError
	switch {
	case err == nil:
		return reply, true, nil
	case errors.As(err, &v):
		r, err := t.reask(ctx, act, tx, act.Awaiting, act.SuggestedCategoryID, v.Msg)
		return r, true, err
	}
	return "", false, err
}
