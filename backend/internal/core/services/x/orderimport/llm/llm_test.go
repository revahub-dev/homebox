package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDisabledWhenUnset(t *testing.T) {
	assert.Nil(t, New(Config{}))
	assert.Nil(t, New(Config{BaseURL: "http://x"}))
	assert.NotNil(t, New(Config{BaseURL: "http://x", Model: "m"}))
}

func fakeServer(t *testing.T, reply string, check func(map[string]any)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		b, _ := io.ReadAll(r.Body)
		var req map[string]any
		assert.NoError(t, json.Unmarshal(b, &req))
		if check != nil {
			check(req)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
		})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestExtractText(t *testing.T) {
	s := fakeServer(t, `{"store":"example","order_number":"A-0001","order_date":"2026-10-14","total":1290,"currency":"jpy",
	  "items":[{"name":"無印良品 アクリル仕切りスタンド","quantity":1,"unit_price":1290,"asin":"bad asin","jan":"4550002000000"},{"name":""}]}`,
		func(req map[string]any) {
			assert.Equal(t, "text-model", req["model"])
			assert.Contains(t, req["messages"].([]any)[1].(map[string]any)["content"], "Subject: ご注文")
		})
	x := New(Config{BaseURL: s.URL + "/v1/", APIKey: "sk-test", Model: "text-model"})
	o, err := x.ExtractText(context.Background(), "ご注文", strings.Repeat("あ", 30000))
	require.NoError(t, err)
	assert.Equal(t, "A-0001", o.OrderNumber)
	assert.Equal(t, "JPY", o.Currency)
	assert.Equal(t, "2026-10-14", o.OrderDate.Format("2006-01-02"))
	require.Len(t, o.Items, 1)
	assert.Empty(t, o.Items[0].ASIN, "invalid ASIN dropped")
	assert.Equal(t, "4550002000000", o.Items[0].JAN)
}

func TestExtractImageUsesVisionModelAndDataURL(t *testing.T) {
	s := fakeServer(t, "```json\n{\"items\":[{\"name\":\"レシート品\",\"quantity\":0,\"unit_price\":100}]}\n```", func(req map[string]any) {
		assert.Equal(t, "vision-model", req["model"])
		parts := req["messages"].([]any)[1].(map[string]any)["content"].([]any)
		assert.True(t, strings.HasPrefix(parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,"))
	})
	x := New(Config{BaseURL: s.URL + "/v1", APIKey: "sk-test", Model: "text-model", VisionModel: "vision-model"})
	o, err := x.ExtractImage(context.Background(), "image/png", []byte("png"))
	require.NoError(t, err)
	require.Len(t, o.Items, 1)
	assert.InDelta(t, 1, o.Items[0].Quantity, 0.001)
}

func TestValidateRejectsGarbage(t *testing.T) {
	_, err := Validate("sorry, I can't")
	require.ErrorIs(t, err, ErrInvalidOutput)
	_, err = Validate(`{"total":-1}`)
	require.ErrorIs(t, err, ErrInvalidOutput)
}

func TestServerErrorIsReported(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer s.Close()
	_, err := New(Config{BaseURL: s.URL, Model: "m"}).ExtractText(context.Background(), "", "x")
	require.Error(t, err)
}
