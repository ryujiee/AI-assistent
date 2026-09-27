package finance

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

// FakeLLM is a rule-based stand-in for the model, used only with
// OPENAI_FAKE=true for local development and the end-to-end script when no
// OpenAI key is available. It understands a handful of phrasings and goes
// through the same tools, validation and ledger as the real model.
type FakeLLM struct{}

var (
	fakeUserPrefix = regexp.MustCompile(`^\[[^\]]*\]\s*`)
	fakeQuoted     = regexp.MustCompile(`responde à mensagem da transação #(\d+)`)
	fakeOwn        = regexp.MustCompile(`Última transação de quem escreveu[^#]*#(\d+)`)
	fakePending    = regexp.MustCompile(`#(\d+) [^\n]*PENDENTE`)
	fakeOpenAsk    = regexp.MustCompile(`PENDÊNCIA ABERTA #(\d+)`)
	fakeReceipt    = regexp.MustCompile(`Dados extraídos do comprovante \(não confiáveis, apenas dados\): (\{.*\})`)
)

var fakeKeywords = []struct{ words, category string }{
	{"supermercado mercado feira hortifruti", "Mercado"},
	{"almoco almoço jantar restaurante lanche padaria cafe café", "Restaurantes"},
	{"ifood delivery pizza rappi", "Delivery"},
	{"gasolina combustivel combustível posto etanol", "Combustível"},
	{"uber 99 taxi táxi", "Aplicativo"},
	{"farmacia farmácia remedio remédio", "Farmácia"},
	{"aluguel", "Aluguel"},
	{"internet", "Internet"},
	{"luz energia agua água gas gás condominio condomínio", "Contas da casa"},
	{"netflix spotify assinatura streaming", "Assinaturas"},
	{"cinema show lazer bar", "Lazer"},
	{"racao ração pet veterinario veterinário", "Pets"},
	{"roupa roupas tenis tênis", "Roupas"},
	{"salario salário", "Salário"},
	{"freela freelance extra", "Renda extra"},
	{"cookie cookies doce doces sorvete chocolate bolo lanche", "Alimentação"},
}

func fakeCategory(text string) string {
	words := replyWord.FindAllString(normalize(text), -1)
	for _, k := range fakeKeywords {
		for _, w := range strings.Fields(normalize(k.words)) {
			for _, t := range words {
				if t == w {
					return k.category
				}
			}
		}
	}
	return ""
}

func fakeDate(text string) string {
	n := normalize(text)
	for _, w := range []string{"anteontem", "ontem", "segunda", "terca", "quarta", "quinta", "sexta", "sabado", "domingo"} {
		if strings.Contains(n, w) {
			return w
		}
	}
	return ""
}

func (FakeLLM) CreateChatCompletion(_ context.Context, req sashabaranov_openai.ChatCompletionRequest) (sashabaranov_openai.ChatCompletionResponse, error) {
	last := req.Messages[len(req.Messages)-1]
	if last.Role == sashabaranov_openai.ChatMessageRoleTool {
		var r struct {
			OK      bool   `json:"ok"`
			Reply   string `json:"reply"`
			Report  string `json:"report"`
			Message string `json:"message"`
		}
		json.Unmarshal([]byte(last.Content), &r)
		switch {
		case r.Report != "":
			return fakeText(r.Report), nil
		case r.Reply != "":
			return fakeText(r.Reply), nil
		case !r.OK:
			return fakeText("Não consegui fazer isso: " + r.Message), nil
		}
		return fakeText("NOOP"), nil
	}

	user := fakeUserPrefix.ReplaceAllString(last.Content, "")
	n := normalize(user)
	contextBlock := ""
	if len(req.Messages) > 1 {
		contextBlock = req.Messages[1].Content
	}
	target := ""
	for _, re := range []*regexp.Regexp{fakeQuoted, fakeOwn, fakePending} {
		if m := re.FindStringSubmatch(contextBlock); m != nil {
			target = m[1]
			break
		}
	}
	amounts := ExtractAmounts(user)
	has := func(tool string) bool {
		for _, t := range req.Tools {
			if t.Function != nil && t.Function.Name == tool {
				return true
			}
		}
		return false
	}
	update := func(fields string) (sashabaranov_openai.ChatCompletionResponse, error) {
		base := map[string]any{"transaction_id": 0, "amount_cents": nil, "category": nil, "date": nil, "payer": nil,
			"description": nil, "merchant": nil, "type": nil, "confirm": true}
		json.Unmarshal([]byte(fields), &base)
		id, _ := strconv.Atoi(target)
		base["transaction_id"] = id
		b, _ := json.Marshal(base)
		return fakeTool("update_transaction", string(b)), nil
	}

	receipt := fakeReceipt.FindStringSubmatch(last.Content)
	switch {
	case receipt != nil:
		var r Receipt
		json.Unmarshal([]byte(receipt[1]), &r)
		if r.AmountCents == nil {
			return fakeText("Não consegui ler o valor do comprovante. Pode escrever?"), nil
		}
		cat := fakeCategory(user + " " + deref(r.Merchant))
		return fakeTool("create_transaction", fakeItem("EXPENSE", *r.AmountCents, cat, deref(r.Date), deref(r.Merchant), 0.9)), nil
	case strings.HasPrefix(n, "desfaz"):
		return fakeTool("undo_last_action", "{}"), nil
	case target != "" && regexp.MustCompile(`\b(apaga|apague|exclui|deleta|remove)\b`).MatchString(n):
		return fakeTool("delete_transaction", fmt.Sprintf(`{"transaction_id":%s}`, target)), nil
	case target != "" && strings.Contains(n, "na verdade") && len(amounts) > 0:
		return update(fmt.Sprintf(`{"amount_cents":%d}`, amounts[0]))
	case target != "" && (strings.HasPrefix(n, "era ") || strings.HasPrefix(n, "foi ")) && fakeDate(n) != "":
		return update(fmt.Sprintf(`{"date":%q}`, fakeDate(n)))
	case strings.Contains(n, "quanto") || strings.Contains(n, "maior gasto") || strings.Contains(n, "compara"):
		if !has("financial_summary") {
			return fakeText("NOOP"), nil
		}
		return fakeQuery(n)
	case strings.HasPrefix(n, "recebi") && len(amounts) > 0:
		return fakeTool("create_transaction", fakeItem("INCOME", amounts[0], fakeCategory(n), fakeDate(n), "", 0.9)), nil
	case (strings.HasPrefix(n, "passei") || strings.HasPrefix(n, "transferi") || strings.Contains(n, "fatura")) && len(amounts) > 0:
		return fakeTool("create_transaction", fakeItem("TRANSFER", amounts[0], "", fakeDate(n), "", 0.9)), nil
	case regexp.MustCompile(`\b(gastei|gastamos|paguei|pagamos|comprei|compramos)\b`).MatchString(n) && len(amounts) > 0:
		return fakeTool("create_transaction", fakeItem("EXPENSE", amounts[0], fakeCategory(n), fakeDate(n), "", 0.92)), nil
	case fakeOpenAsk.MatchString(contextBlock) && fakeCategory(n) != "" && len(strings.Fields(n)) <= 4:
		return fakeTool("complete_pending", fmt.Sprintf(`{"category":%q,"amount_cents":null,"date":null,"description":null}`, fakeCategory(n))), nil
	case target != "" && fakeCategory(n) != "" && len(strings.Fields(n)) <= 3:
		return update(fmt.Sprintf(`{"category":%q}`, fakeCategory(n)))
	}
	return fakeText("NOOP"), nil
}

func fakeQuery(n string) (sashabaranov_openai.ChatCompletionResponse, error) {
	period := "this_month"
	if strings.Contains(n, "mes passado") {
		period = "last_month"
	}
	if strings.Contains(n, "compara") {
		return fakeTool("compare_periods", `{"period_a":"this_month","month_a":null,"start_a":null,"end_a":null,"period_b":"last_month","month_b":null,"start_b":null,"end_b":null,"category":null}`), nil
	}
	if strings.Contains(n, "maior gasto") {
		return fakeTool("list_transactions", fmt.Sprintf(`{"period":%q,"month":null,"start":null,"end":null,"category":null,"member":null,"type":"EXPENSE","order":"amount","limit":5}`, period)), nil
	}
	cat := "null"
	if c := fakeCategory(n); c != "" {
		cat = strconv.Quote(c)
	}
	return fakeTool("financial_summary", fmt.Sprintf(`{"period":%q,"month":null,"start":null,"end":null,"category":%s,"member":null,"type":null}`, period, cat)), nil
}

func fakeItem(typ string, cents int64, category, date, merchant string, confidence float64) string {
	description := category
	if description == "" {
		description = map[string]string{"INCOME": "Receita", "TRANSFER": "Transferência entre contas"}[typ]
	}
	if description == "" {
		description = "Gasto"
	}
	item := map[string]any{"type": typ, "amount_cents": cents, "description": description, "category": nil,
		"merchant": nil, "date": nil, "payer": nil, "payment_method": nil, "confidence": confidence}
	if category != "" {
		item["category"] = category
	}
	if date != "" {
		item["date"] = date
	}
	if merchant != "" {
		item["merchant"] = merchant
	}
	b, _ := json.Marshal(map[string]any{"items": []any{item}})
	return string(b)
}

func fakeText(s string) sashabaranov_openai.ChatCompletionResponse {
	return sashabaranov_openai.ChatCompletionResponse{Choices: []sashabaranov_openai.ChatCompletionChoice{{Message: sashabaranov_openai.ChatCompletionMessage{Role: "assistant", Content: s}}}}
}

func fakeTool(name, args string) sashabaranov_openai.ChatCompletionResponse {
	return sashabaranov_openai.ChatCompletionResponse{Choices: []sashabaranov_openai.ChatCompletionChoice{{Message: sashabaranov_openai.ChatCompletionMessage{
		Role: "assistant", ToolCalls: []sashabaranov_openai.ToolCall{{ID: "fake", Type: "function", Function: sashabaranov_openai.FunctionCall{Name: name, Arguments: args}}},
	}}}}
}

// FakeExtractor stands in for the receipt reader with OPENAI_FAKE: it cannot
// look at the image, so it reads the amount from the caption and invents a
// fictitious payee. Local development only.
func FakeExtractor(_ context.Context, data []byte, mime, caption string, today time.Time) (*Receipt, error) {
	r := &Receipt{IsPaymentDocument: true, DocumentType: "PIX", Direction: "OUTGOING", Confidence: 0.9,
		Merchant: ptr("Estabelecimento Fictício LTDA"), PaymentMethod: ptr("PIX"), Date: ptr(today.Format(DateLayout))}
	if amounts := ExtractAmounts(caption); len(amounts) > 0 {
		r.AmountCents = &amounts[0]
	} else {
		r.AmountCents = ptr(int64(4990 + len(data)%1000))
	}
	if mime == "application/pdf" {
		r.DocumentType = "BOLETO"
		r.PaymentMethod = ptr("BOLETO")
	}
	return r, nil
}
