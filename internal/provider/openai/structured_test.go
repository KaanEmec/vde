package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestStructuredOutput: a request with a Schema (structured_output native) carries it as
// response_format json_schema, strict; without one, or with a schema whose root is not an
// object, the body is the prompt-only body byte for byte.
func TestStructuredOutput(t *testing.T) {
	t.Parallel()
	c := New(endpoint(t, "http://127.0.0.1:1/v1", "", ""))
	plain := request()
	want, err := c.buildBody(plain)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		schema string
		native bool
	}{
		{name: "no schema", schema: ""},
		{name: "object schema", schema: `{"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}`, native: true},
		{name: "array root", schema: `{"type": "array", "items": {"type": "object"}}`},
		{name: "type list root", schema: `{"type": ["object", "null"]}`},
		{name: "no root type", schema: `{"properties": {"a": {}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request()
			if tt.schema != "" {
				req.Schema = []byte(tt.schema)
			}
			body, err := c.buildBody(req)
			if err != nil {
				t.Fatal(err)
			}
			if !tt.native {
				if !bytes.Equal(body, want) {
					t.Errorf("body differs from the prompt-only body:\n%s\n%s", body, want)
				}
				return
			}
			var b struct {
				ResponseFormat struct {
					Type       string `json:"type"`
					JSONSchema struct {
						Name   string          `json:"name"`
						Strict bool            `json:"strict"`
						Schema json.RawMessage `json:"schema"`
					} `json:"json_schema"`
				} `json:"response_format"`
			}
			if err := json.Unmarshal(body, &b); err != nil {
				t.Fatal(err)
			}
			rf := b.ResponseFormat
			var gotSchema, wantSchema any
			_ = json.Unmarshal(rf.JSONSchema.Schema, &gotSchema)
			_ = json.Unmarshal([]byte(tt.schema), &wantSchema)
			if rf.Type != "json_schema" || rf.JSONSchema.Name != "answer" || !rf.JSONSchema.Strict ||
				!jsonEqual(gotSchema, wantSchema) {
				t.Errorf("response_format = %+v", rf)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// TestStructuredOutputRefused: an endpoint that refuses the constraint with 400 gets the
// request once more without it; a 400 on a prompt-only request is not retried.
func TestStructuredOutputRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		schema     string
		statuses   []int
		wantBodies int
		wantErr    bool
	}{
		{name: "refused then prompt-only", schema: `{"type": "object"}`, statuses: []int{400, 200}, wantBodies: 2},
		{name: "refused twice", schema: `{"type": "object"}`, statuses: []int{400, 400}, wantBodies: 2, wantErr: true},
		{name: "prompt-only 400", statuses: []int{400}, wantBodies: 1, wantErr: true},
		{name: "constraint 500 not retried here", schema: `{"type": "object"}`, statuses: []int{500}, wantBodies: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var bodies [][]byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				n := len(bodies)
				bodies = append(bodies, body)
				mu.Unlock()
				status := tt.statuses[min(n, len(tt.statuses)-1)]
				w.WriteHeader(status)
				if status == 200 {
					_, _ = io.WriteString(w, okReply)
				}
			}))
			t.Cleanup(srv.Close)
			req := request()
			if tt.schema != "" {
				req.Schema = []byte(tt.schema)
			}
			_, err := New(endpoint(t, srv.URL, "", "")).Complete(t.Context(), req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Complete error = %v, want error %v", err, tt.wantErr)
			}
			if len(bodies) != tt.wantBodies {
				t.Fatalf("%d requests, want %d", len(bodies), tt.wantBodies)
			}
			if tt.wantBodies == 2 && (!bytes.Contains(bodies[0], []byte(`"response_format"`)) ||
				bytes.Contains(bodies[1], []byte(`"response_format"`))) {
				t.Error("want the constraint on the first request only")
			}
		})
	}
}
