package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStructuredRequestShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Error("missing auth header")
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer srv.Close()
	c := &StructuredClient{APIKey: "k", BaseURL: srv.URL}

	for _, tc := range []struct{ mime, part string }{{"image/jpeg", "image_url"}, {"application/pdf", "file"}} {
		out, err := c.Complete(context.Background(), StructuredRequest{Model: "m", System: "s", Text: "t", File: []byte("data"), Mime: tc.mime, FileName: "f", SchemaName: "x", Schema: map[string]any{"type": "object"}})
		if err != nil || string(out) != `{"ok":true}` {
			t.Fatalf("out=%s err=%v", out, err)
		}
		if _, hasTools := got["tools"]; hasTools {
			t.Fatal("extraction request carries tools")
		}
		rf := got["response_format"].(map[string]any)["json_schema"].(map[string]any)
		if rf["strict"] != true {
			t.Fatal("schema not strict")
		}
		content := got["messages"].([]any)[1].(map[string]any)["content"].([]any)
		part := content[1].(map[string]any)
		if part["type"] != tc.part {
			t.Fatalf("%s sent as %v", tc.mime, part["type"])
		}
		if tc.part == "file" && !strings.HasPrefix(part["file"].(map[string]any)["file_data"].(string), "data:application/pdf;base64,") {
			t.Fatal("pdf not sent as base64 file_data")
		}
	}
}

func TestStructuredErrorsDoNotLeakBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat") && r.Header.Get("X-Refuse") == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"your secret receipt text"}}`))
		}
	}))
	defer srv.Close()
	_, err := (&StructuredClient{APIKey: "k", BaseURL: srv.URL}).Complete(context.Background(), StructuredRequest{Model: "m"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}

	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"","refusal":"no"}}]}`))
	}))
	defer refuse.Close()
	if _, err := (&StructuredClient{APIKey: "k", BaseURL: refuse.URL}).Complete(context.Background(), StructuredRequest{Model: "m"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("refusal err = %v", err)
	}
}
