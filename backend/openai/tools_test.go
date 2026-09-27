package openai

import (
	"context"
	"errors"
	"testing"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

type scripted struct {
	responses []sashabaranov_openai.ChatCompletionResponse
	errs      []error
	models    []string
}

func (s *scripted) CreateChatCompletion(_ context.Context, req sashabaranov_openai.ChatCompletionRequest) (sashabaranov_openai.ChatCompletionResponse, error) {
	s.models = append(s.models, req.Model)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return sashabaranov_openai.ChatCompletionResponse{}, err
		}
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func toolCall(name, args string) sashabaranov_openai.ChatCompletionResponse {
	return sashabaranov_openai.ChatCompletionResponse{Choices: []sashabaranov_openai.ChatCompletionChoice{{Message: sashabaranov_openai.ChatCompletionMessage{
		Role: "assistant", ToolCalls: []sashabaranov_openai.ToolCall{{ID: "c1", Type: "function", Function: sashabaranov_openai.FunctionCall{Name: name, Arguments: args}}},
	}}}}
}

func text(s string) sashabaranov_openai.ChatCompletionResponse {
	return sashabaranov_openai.ChatCompletionResponse{Choices: []sashabaranov_openai.ChatCompletionChoice{{Message: sashabaranov_openai.ChatCompletionMessage{Role: "assistant", Content: s}}}}
}

func TestRunToolsExecutesToolsAndReturnsText(t *testing.T) {
	c := &scripted{responses: []sashabaranov_openai.ChatCompletionResponse{toolCall("save_note", `{"texto":"x"}`), text("feito")}}
	var calls []string
	out, err := RunTools(context.Background(), ToolRun{Client: c, Model: "m", Execute: func(_ context.Context, name, args string) string {
		calls = append(calls, name+args)
		return "ok"
	}})
	if err != nil || out != "feito" || len(calls) != 1 || calls[0] != `save_note{"texto":"x"}` {
		t.Fatalf("out=%q err=%v calls=%v", out, err, calls)
	}
}

func TestRunToolsStopsAtLoopLimit(t *testing.T) {
	c := &scripted{}
	for i := 0; i < 3; i++ {
		c.responses = append(c.responses, toolCall("x", "{}"))
	}
	_, err := RunTools(context.Background(), ToolRun{Client: c, Model: "m", MaxLoops: 3, Execute: func(context.Context, string, string) string { return "" }})
	if !errors.Is(err, ErrToolLoopLimit) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunToolsFallsBack(t *testing.T) {
	c := &scripted{errs: []error{errors.New("down"), nil}, responses: []sashabaranov_openai.ChatCompletionResponse{text("ok")}}
	out, err := RunTools(context.Background(), ToolRun{Client: c, Model: "primary", Fallback: "backup", Execute: nil})
	if err != nil || out != "ok" || len(c.models) != 2 || c.models[1] != "backup" {
		t.Fatalf("out=%q err=%v models=%v", out, err, c.models)
	}
}
