package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisabledWhenUnset(t *testing.T) {
	assert.Nil(t, New(Config{}))
	assert.Nil(t, New(Config{BaseURL: "http://x"}))
}

func TestChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		var req map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "m", req["model"])
		assert.NotNil(t, req["response_format"])
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + "```json\\n{\\\"a\\\":1}\\n```" + `"}}]}`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL + "/v1/", APIKey: "k", Model: "m"})
	require.NotNil(t, c)
	out, err := c.Chat(context.Background(), "s", "u", true)
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, ExtractJSON(out))
}

func TestChatError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := New(Config{BaseURL: srv.URL, Model: "m"}).Chat(context.Background(), "s", "u", false)
	require.Error(t, err)
}

func TestChatVisionAndFormats(t *testing.T) {
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		seen = append(seen, req)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "text", VisionModel: "vis", ResponseFormat: "json_schema"}).(VisionChatter)
	_, err := c.ChatVision(context.Background(), "s", "u", []Image{{MIME: "image/jpeg", Data: []byte("x")}}, true)
	require.NoError(t, err)
	assert.Equal(t, "vis", seen[0]["model"])
	rf := seen[0]["response_format"].(map[string]any)
	assert.Equal(t, "json_schema", rf["type"])
	msgs := seen[0]["messages"].([]any)
	parts := msgs[1].(map[string]any)["content"].([]any)
	assert.Equal(t, "image_url", parts[1].(map[string]any)["type"])

	// "none" sends no response_format
	seen = nil
	c = New(Config{BaseURL: srv.URL, Model: "text", ResponseFormat: "none"}).(VisionChatter)
	_, err = c.Chat(context.Background(), "s", "u", true)
	require.NoError(t, err)
	assert.Nil(t, seen[0]["response_format"])
	assert.Equal(t, "text", seen[0]["model"])
}

func TestRetriesWithoutResponseFormatOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["response_format"] != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Model: "m"})
	out, err := c.Chat(context.Background(), "s", "u", true)
	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, 2, calls)
	// the rejection is remembered: the next call goes straight without the format
	_, err = c.Chat(context.Background(), "s", "u", true)
	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}
