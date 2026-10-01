package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"secretary/db"
	"secretary/timeutil"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

var (
	client                *sashabaranov_openai.Client
	RegisterTimerCallback func(id int, duration time.Duration, jid string, motivo string)

	// chatLocks serializes the turns of each conversation, so messages sent in
	// quick succession are answered in order and see each other in the history.
	chatLocks sync.Map // jid -> *sync.Mutex
)

const (
	// requestTimeout bounds every HTTP call to OpenAI (the default client has none).
	requestTimeout = 90 * time.Second
	// turnTimeout bounds one whole secretary turn, tool calls included.
	turnTimeout = 2 * time.Minute
	// maxTimerMinutes caps timers at one week.
	maxTimerMinutes = 7 * 24 * 60
)

func InitOpenAI(apiKey string) {
	if apiKey == "" {
		log.Println("WARNING: OpenAI API Key is empty. AI assistant will not function correctly.")
	}
	cfg := sashabaranov_openai.DefaultConfig(apiKey)
	cfg.HTTPClient = &http.Client{Timeout: requestTimeout}
	client = sashabaranov_openai.NewClientWithConfig(cfg)
	log.Println("OpenAI Client initialized successfully")
}

func lockChat(jid string) func() {
	v, _ := chatLocks.LoadOrStore(jid, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// ProcessMessageMultimodal handles the OpenAI conversation loop, supports images, executes function calls, and returns the response.
func ProcessMessageMultimodal(jid string, userMessage string, imageBytes []byte, imageMime string) (string, error) {
	if client == nil {
		return "Desculpe, o módulo de Inteligência Artificial não está configurado.", fmt.Errorf("openai client not initialized")
	}
	defer lockChat(jid)()
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()

	var userContentParts []sashabaranov_openai.ChatMessagePart
	var savedContent string

	if len(imageBytes) > 0 {
		base64Img := base64.StdEncoding.EncodeToString(imageBytes)
		if imageMime == "" {
			imageMime = "image/jpeg"
		}

		userContentParts = []sashabaranov_openai.ChatMessagePart{
			{
				Type: sashabaranov_openai.ChatMessagePartTypeText,
				Text: userMessage,
			},
			{
				Type: sashabaranov_openai.ChatMessagePartTypeImageURL,
				ImageURL: &sashabaranov_openai.ChatMessageImageURL{
					URL: "data:" + imageMime + ";base64," + base64Img,
				},
			},
		}

		if userMessage != "" {
			savedContent = "[Imagem] " + userMessage
		} else {
			savedContent = "[Imagem]"
		}
	} else {
		savedContent = userMessage
	}

	// 1. Fetch the recent conversation before storing this message, so the
	// current message is sent to the model exactly once (appended below).
	history, err := db.GetChatHistory(jid, 15)
	if err != nil {
		log.Printf("Failed to load chat history: %v", err)
	}

	// 2. Save user message in database history
	if err := db.SaveChatMessage(jid, "user", savedContent); err != nil {
		log.Printf("Failed to save user message to chat history: %v", err)
	}

	// 3. Setup dynamic System Prompt
	localTimeStr := timeutil.Now().Format("Monday, 02/01/2006 15:04 (MST)")
	systemPrompt := fmt.Sprintf(`Você é a "Secretária Pessoal de IA", uma assistente executiva inteligente, prestativa e organizada.
Você fala com o usuário diretamente no WhatsApp.
Sempre responda de forma educada, amigável, clara e concisa. Use emojis moderadamente e utilize a formatação do WhatsApp (ex: *negrito* para dar ênfase).

A hora local atual é: %s. O fuso horário do usuário é sempre o de Brasília (America/Sao_Paulo, UTC-03:00). Use essa informação para calcular datas relativas como "amanhã", "daqui a duas horas", "próxima segunda-feira", etc. Ao chamar uma ferramenta que recebe data e hora, informe sempre o horário de Brasília, de preferência com o offset explícito (ex: 2026-06-18T14:30:00-03:00). Nunca converta para UTC.

Você tem acesso a ferramentas/funções para gerenciar o calendário, lembretes, timers e bloco de notas do usuário. Sempre que o usuário solicitar uma dessas ações, use a ferramenta correspondente. Após executar uma ferramenta, explique o que foi feito de forma simpática.`, localTimeStr)

	messages := []sashabaranov_openai.ChatCompletionMessage{
		{
			Role:    sashabaranov_openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		},
	}

	// Add historical context to messages list
	for _, hMsg := range history {
		role := sashabaranov_openai.ChatMessageRoleUser
		if hMsg.Role == "assistant" {
			role = sashabaranov_openai.ChatMessageRoleAssistant
		} else if hMsg.Role == "system" {
			role = sashabaranov_openai.ChatMessageRoleSystem
		}
		messages = append(messages, sashabaranov_openai.ChatCompletionMessage{
			Role:    role,
			Content: hMsg.Content,
		})
	}

	// Add current user message
	if len(userContentParts) > 0 {
		messages = append(messages, sashabaranov_openai.ChatCompletionMessage{
			Role:         sashabaranov_openai.ChatMessageRoleUser,
			MultiContent: userContentParts,
		})
	} else {
		messages = append(messages, sashabaranov_openai.ChatCompletionMessage{
			Role:    sashabaranov_openai.ChatMessageRoleUser,
			Content: userMessage,
		})
	}

	// 4. Define Tools (Function Calling)
	tools := []sashabaranov_openai.Tool{
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "create_appointment",
				Description: "Cria um compromisso no calendário/agenda.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"titulo": map[string]interface{}{
							"type":        "string",
							"description": "Título do compromisso (ex: Reunião de Planejamento, Dentista, Academia)",
						},
						"descricao": map[string]interface{}{
							"type":        "string",
							"description": "Detalhes ou notas sobre o compromisso (opcional)",
						},
						"data_hora": map[string]interface{}{
							"type":        "string",
							"description": "Data e hora de início em formato ISO 8601 (ex: 2026-06-18T14:30:00-03:00)",
						},
					},
					"required": []interface{}{"titulo", "data_hora"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "create_timer",
				Description: "Cria um alerta rápido/timer em minutos.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"minutos": map[string]interface{}{
							"type":        "integer",
							"description": "Duração do timer em minutos (ex: 5, 10, 60)",
						},
						"motivo": map[string]interface{}{
							"type":        "string",
							"description": "Motivo do timer (ex: desligar o forno, tomar remédio, ligar para o cliente)",
						},
					},
					"required": []interface{}{"minutos", "motivo"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "save_note",
				Description: "Salva uma informação ou anotação rápida no bloco de notas.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"texto": map[string]interface{}{
							"type":        "string",
							"description": "O texto ou informação a ser guardada",
						},
					},
					"required": []interface{}{"texto"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "search_notes_and_calendar",
				Description: "Pesquisa nos compromissos do calendário e bloco de notas do usuário por informações anteriores.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type":        "string",
							"description": "Termo de busca ou palavra-chave para pesquisar",
						},
					},
					"required": []interface{}{"query"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "add_to_shopping_list",
				Description: "Adiciona um ou mais itens à lista de compras do usuário. Trate como aviso/erro se duplicar.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"itens": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "string",
							},
							"description": "Lista de itens a serem adicionados (ex: ['leite', 'pão', 'ovos'])",
						},
					},
					"required": []interface{}{"itens"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "get_shopping_list",
				Description: "Recupera e lista todos os itens atualmente salvos na lista de compras.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "remove_from_shopping_list",
				Description: "Remove um ou mais itens da lista de compras do usuário.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"itens": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "string",
							},
							"description": "Lista de itens a serem removidos (ex: ['leite', 'banana'])",
						},
					},
					"required": []interface{}{"itens"},
				},
			},
		},
		{
			Type: sashabaranov_openai.ToolTypeFunction,
			Function: &sashabaranov_openai.FunctionDefinition{
				Name:        "clear_shopping_list",
				Description: "Limpa completamente todos os itens da lista de compras.",
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		},
	}

	// 5. OpenAI Tool Call Loop
	content, err := RunTools(ctx, ToolRun{
		Client:   client,
		Model:    sashabaranov_openai.GPT4o,
		Fallback: sashabaranov_openai.GPT4oMini,
		Messages: messages,
		Tools:    tools,
		MaxLoops: 5,
		Execute: func(_ context.Context, name, args string) string {
			switch name {
			case "create_appointment":
				return executeCreateAppointment(args)
			case "create_timer":
				return executeCreateTimer(args, jid)
			case "save_note":
				return executeSaveNote(args)
			case "search_notes_and_calendar":
				return executeSearchNotesAndCalendar(args)
			case "add_to_shopping_list":
				return executeAddToShoppingList(args)
			case "get_shopping_list":
				return executeGetShoppingList()
			case "remove_from_shopping_list":
				return executeRemoveFromShoppingList(args)
			case "clear_shopping_list":
				return executeClearShoppingList()
			}
			return fmt.Sprintf("Erro: Ferramenta %s desconhecida", name)
		},
	})
	if errors.Is(err, ErrToolLoopLimit) {
		return "Desculpe, o processamento da sua solicitação excedeu o limite de etapas internas.", err
	}
	if err != nil {
		return "", err
	}

	// Save assistant message to chat history
	if err := db.SaveChatMessage(jid, "assistant", content); err != nil {
		log.Printf("Failed to save assistant message to history: %v", err)
	}
	return content, nil
}

// ComposeMessage asks the model for a single message, without tools and
// without touching the chat history (used by scheduled summaries).
func ComposeMessage(prompt string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("openai client not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), turnTimeout)
	defer cancel()
	return RunTools(ctx, ToolRun{
		Client:   client,
		Model:    sashabaranov_openai.GPT4o,
		Fallback: sashabaranov_openai.GPT4oMini,
		Messages: []sashabaranov_openai.ChatCompletionMessage{{Role: sashabaranov_openai.ChatMessageRoleUser, Content: prompt}},
		MaxLoops: 1,
	})
}

// TranscribeAudio calls the OpenAI Whisper API to transcribe audio bytes to text.
func TranscribeAudio(ctx context.Context, audioBytes []byte) (string, error) {
	if client == nil {
		return "", fmt.Errorf("openai client not initialized")
	}

	tempFile, err := os.CreateTemp("", "audio-*.ogg")
	if err != nil {
		return "", fmt.Errorf("failed to create temp audio file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := tempFile.Write(audioBytes); err != nil {
		return "", fmt.Errorf("failed to write audio bytes: %w", err)
	}
	tempFile.Close()

	req := sashabaranov_openai.AudioRequest{
		Model:    sashabaranov_openai.Whisper1,
		FilePath: tempFile.Name(),
	}

	resp, err := client.CreateTranscription(ctx, req)
	if err != nil {
		return "", fmt.Errorf("whisper transcription failed: %w", err)
	}

	return resp.Text, nil
}

// Tool Executors
func executeCreateAppointment(args string) string {
	type Params struct {
		Titulo    string `json:"titulo"`
		Descricao string `json:"descricao"`
		DataHora  string `json:"data_hora"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}
	p.Titulo = strings.TrimSpace(p.Titulo)
	if p.Titulo == "" {
		return "Erro: o compromisso precisa de um título."
	}

	// Dates without an explicit offset are read as a Brasília wall clock, which
	// is how the user states appointments in the chat.
	parsedTime, err := timeutil.ParseLocal(p.DataHora)
	if err != nil {
		return fmt.Sprintf("Erro: Formato de data inválido '%s'. Use ISO 8601.", p.DataHora)
	}

	id, err := db.CreateAppointment(p.Titulo, p.Descricao, parsedTime)
	if err != nil {
		return toolFailure("create_appointment", err)
	}

	return fmt.Sprintf("Sucesso: Compromisso '%s' agendado para %s (ID: %d). Um lembrete foi configurado para 15 minutos antes.", p.Titulo, parsedTime.Format("02/01/2006 às 15:04"), id)
}

func executeCreateTimer(args string, jid string) string {
	type Params struct {
		Minutos int    `json:"minutos"`
		Motivo  string `json:"motivo"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}
	if p.Minutos < 1 || p.Minutos > maxTimerMinutes {
		return fmt.Sprintf("Erro: o timer precisa ter entre 1 e %d minutos.", maxTimerMinutes)
	}

	duration := time.Duration(p.Minutos) * time.Minute
	dispararEm := timeutil.Now().Add(duration)

	id, err := db.CreateTimer(p.Minutos*60, dispararEm, p.Motivo)
	if err != nil {
		return toolFailure("create_timer", err)
	}

	// Register the timer dynamically in the Go background engine
	if RegisterTimerCallback != nil {
		RegisterTimerCallback(id, duration, jid, p.Motivo)
	}

	return fmt.Sprintf("Sucesso: Timer de %d minutos criado para '%s' (ID: %d). Eu te avisarei assim que o tempo acabar!", p.Minutos, p.Motivo, id)
}

func executeSaveNote(args string) string {
	type Params struct {
		Texto string `json:"texto"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}

	if err := db.SaveNote(p.Texto); err != nil {
		return toolFailure("save_note", err)
	}

	return "Sucesso: Nota salva com sucesso no bloco de notas."
}

func executeSearchNotesAndCalendar(args string) string {
	type Params struct {
		Query string `json:"query"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}

	results, err := db.SearchNotesAndCalendar(p.Query)
	if err != nil {
		return toolFailure("search_notes_and_calendar", err)
	}

	if len(results) == 0 {
		return fmt.Sprintf("Resultado: Nenhuma nota ou compromisso encontrado para a busca '%s'.", p.Query)
	}

	summary := fmt.Sprintf("Resultado da busca por '%s':\n", p.Query)
	for _, res := range results {
		summary += fmt.Sprintf("- %s\n", res)
	}
	return summary
}

func executeAddToShoppingList(args string) string {
	type Params struct {
		Itens []string `json:"itens"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}

	added, duplicates, err := db.AddShoppingListItems(p.Itens)
	if err != nil {
		return toolFailure("add_to_shopping_list", err)
	}

	result := ""
	if len(added) > 0 {
		result += fmt.Sprintf("Adicionado(s) com sucesso: %s. ", strings.Join(added, ", "))
	}
	if len(duplicates) > 0 {
		result += fmt.Sprintf("Aviso: Você já possui o(s) item(ns) '%s' na sua lista de compras.", strings.Join(duplicates, ", "))
	}
	if result == "" {
		result = "Nenhum item válido foi processado."
	}
	return result
}

func executeGetShoppingList() string {
	list, err := db.GetShoppingList()
	if err != nil {
		return toolFailure("get_shopping_list", err)
	}

	if len(list) == 0 {
		return "A sua lista de compras está vazia."
	}

	result := "Itens na sua lista de compras:\n"
	for _, item := range list {
		result += fmt.Sprintf("- %s\n", item)
	}
	return result
}

func executeRemoveFromShoppingList(args string) string {
	type Params struct {
		Itens []string `json:"itens"`
	}

	var p Params
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "Erro: argumentos inválidos."
	}

	removed, err := db.RemoveShoppingListItems(p.Itens)
	if err != nil {
		return toolFailure("remove_from_shopping_list", err)
	}

	if len(removed) == 0 {
		return "Nenhum dos itens informados estava na lista de compras."
	}

	return fmt.Sprintf("Sucesso: Removido(s) da lista: %s.", strings.Join(removed, ", "))
}

func executeClearShoppingList() string {
	if err := db.ClearShoppingList(); err != nil {
		return toolFailure("clear_shopping_list", err)
	}
	return "Sucesso: A lista de compras foi totalmente limpa."
}

// toolFailure logs the real error and gives the model a generic message, so
// database details never end up in a WhatsApp reply.
func toolFailure(tool string, err error) string {
	log.Printf("tool %s failed: %v", tool, err)
	return "Erro interno ao executar a ação. Peça ao usuário para tentar novamente."
}
