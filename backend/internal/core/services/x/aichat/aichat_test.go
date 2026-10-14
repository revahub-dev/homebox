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
