package openai

import (
	"context"
	"errors"
	"fmt"
	"log"

	sashabaranov_openai "github.com/sashabaranov/go-openai"
)

// ChatCompleter is the part of the OpenAI client the tool loop needs. The
// go-openai client implements it; tests and the local fake do too.
type ChatCompleter interface {
	CreateChatCompletion(ctx context.Context, req sashabaranov_openai.ChatCompletionRequest) (sashabaranov_openai.ChatCompletionResponse, error)
}

// ToolRun describes one conversation turn with function calling.
type ToolRun struct {
	Client ChatCompleter
	Model  string
	// Fallback is tried when a completion with Model fails ("" = no fallback).
	Fallback    string
	Temperature float32
	Messages    []sashabaranov_openai.ChatCompletionMessage
	Tools       []sashabaranov_openai.Tool
	// Execute runs a tool call and returns the text given back to the model.
	// It must validate everything: the model's arguments are untrusted.
	Execute  func(ctx context.Context, name, args string) string
	MaxLoops int
}

// ErrToolLoopLimit is returned when the model keeps calling tools.
var ErrToolLoopLimit = errors.New("reached tool call iteration limit")

// RunTools runs the completion / tool-call loop until the model answers with
// text, and returns that text.
func RunTools(ctx context.Context, run ToolRun) (string, error) {
	if run.Client == nil {
		return "", errors.New("openai client not initialized")
	}
	if run.MaxLoops <= 0 {
		run.MaxLoops = 5
	}
	messages := run.Messages
	for i := 0; i < run.MaxLoops; i++ {
		req := sashabaranov_openai.ChatCompletionRequest{
			Model:       run.Model,
			Messages:    messages,
			Tools:       run.Tools,
			Temperature: run.Temperature,
		}
		resp, err := run.Client.CreateChatCompletion(ctx, req)
		// No fallback when the turn itself was cancelled or timed out.
		if err != nil && run.Fallback != "" && ctx.Err() == nil {
			log.Printf("OpenAI completion error: %v, attempting %s fallback...", err, run.Fallback)
			req.Model = run.Fallback
			resp, err = run.Client.CreateChatCompletion(ctx, req)
		}
		if err != nil {
			return "", fmt.Errorf("openai error: %w", err)
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("openai error: empty response")
		}

		choice := resp.Choices[0]
		messages = append(messages, choice.Message)
		if len(choice.Message.ToolCalls) == 0 {
			return choice.Message.Content, nil
		}
		for _, call := range choice.Message.ToolCalls {
			result := run.Execute(ctx, call.Function.Name, call.Function.Arguments)
			messages = append(messages, sashabaranov_openai.ChatCompletionMessage{
				Role:       sashabaranov_openai.ChatMessageRoleTool,
				Content:    result,
				ToolCallID: call.ID,
			})
		}
	}
	return "", ErrToolLoopLimit
}

// Client returns the shared OpenAI client (nil when not initialized).
func Client() ChatCompleter {
	if client == nil {
		return nil
	}
	return client
}
