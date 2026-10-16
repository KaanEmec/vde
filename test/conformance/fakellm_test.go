package conformance

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	roleExtractor = "extractor"
	roleJudge     = "judge"
)

// fakeLLM is an OpenAI-compatible chat completions server. Every instance has its own
// namespace, the first path segment of its endpoints' base URLs
// (/{namespace}/{endpoint}/v1/chat/completions), with its own script and call record.
type fakeLLM struct {
	srv    *httptest.Server
	mu     sync.Mutex
	spaces map[string]*space
}

type space struct {
	script func(c llmCall) llmReply // nil: defaultScript
	calls  []llmCall
}

// llmCall is one chat completions request as the fake received it.
type llmCall struct {
	Endpoint string // primary, fallback, judge or judge-fallback
	Role     string // from the endpoint
	Model    string
	System   string
	Texts    []string // the user message's text parts, in order
	Blobs    []string // the user message's image_url and file parts (data URLs), in order
}

// text is every text the call carried, system prompt first.
func (c llmCall) text() string { return c.System + "\n" + strings.Join(c.Texts, "\n") }

func (c llmCall) contains(s string) bool { return strings.Contains(c.text(), s) }

// llmReply is the fake's answer to one call.
type llmReply struct {
	Text   string        // the assistant message content
	Status int           // when not 0, answer with this status and an error body instead
	Delay  time.Duration // wait before answering (cut short when the caller gives up)
}

func newFakeLLM() *fakeLLM {
	f := &fakeLLM{spaces: map[string]*space{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeLLM) url() string { return f.srv.URL }

func (f *fakeLLM) close() { f.srv.Close() }

func (f *fakeLLM) space(ns string) *space {
	s, ok := f.spaces[ns]
	if !ok {
		s = &space{}
		f.spaces[ns] = s
	}
	return s
}

// setScript answers the namespace's calls with script until the test ends.
func (f *fakeLLM) setScript(t *testing.T, ns string, script func(c llmCall) llmReply) {
	t.Helper()
	f.mu.Lock()
	f.space(ns).script = script
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		f.space(ns).script = nil
		f.mu.Unlock()
	})
}

// calls returns a copy of the namespace's calls from index from on.
func (f *fakeLLM) calls(ns string, from int) []llmCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.space(ns).calls
	if from > len(all) {
		from = len(all)
	}
	return append([]llmCall(nil), all[from:]...)
}

// count is the number of calls the namespace has received: the provider usage counter.
func (f *fakeLLM) count(ns string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.space(ns).calls)
}

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	File *struct {
		FileData string `json:"file_data"`
	} `json:"file"`
}

func (f *fakeLLM) serve(w http.ResponseWriter, r *http.Request) {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method != http.MethodPost || len(segs) != 5 || segs[2] != "v1" || segs[3] != "chat" || segs[4] != "completions" {
		http.Error(w, `{"error":{"message":"no such route"}}`, http.StatusNotFound)
		return
	}
	ns, endpoint := segs[0], segs[1]
	role := ""
	for _, e := range endpoints {
		if e.name == endpoint {
			role = e.role
		}
	}
	body, err := io.ReadAll(r.Body)
	var req chatRequest
	if err != nil || role == "" || json.Unmarshal(body, &req) != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	call := llmCall{Endpoint: endpoint, Role: role, Model: req.Model}
	for _, m := range req.Messages {
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			if m.Role == "system" {
				call.System = s
			} else {
				call.Texts = append(call.Texts, s)
			}
			continue
		}
		var parts []contentPart
		_ = json.Unmarshal(m.Content, &parts)
		for _, p := range parts {
			switch {
			case p.ImageURL != nil:
				call.Blobs = append(call.Blobs, p.ImageURL.URL)
			case p.File != nil:
				call.Blobs = append(call.Blobs, p.File.FileData)
			default:
				call.Texts = append(call.Texts, p.Text)
			}
		}
	}

	f.mu.Lock()
	sp := f.space(ns)
	sp.calls = append(sp.calls, call)
	script := sp.script
	f.mu.Unlock()
	if script == nil {
		script = defaultScript
	}
	reply := script(call)
	if reply.Delay > 0 {
		select {
		case <-time.After(reply.Delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if reply.Status != 0 {
		w.WriteHeader(reply.Status)
		_, _ = io.WriteString(w, `{"error":{"message":"scripted failure","type":"server_error"}}`)
		return
	}
	resp := map[string]any{
		"id": "chatcmpl-conformance", "object": "chat.completion", "model": req.Model,
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": reply.Text},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// defaultScript accepts a schema-valid candidate on the first attempt.
func defaultScript(c llmCall) llmReply {
	if c.Role == roleExtractor {
		return llmReply{Text: candidate("conformance default candidate")}
	}
	return llmReply{Text: accept}
}

const accept = `{"verdict": "accept", "feedback": {}}`

// candidate is a candidate that validates against the TravelInbox schema, with note in
// its notes field so a test can find it again.
func candidate(note string) string {
	b, _ := json.Marshal(note)
	return `{"schema_version": 1, "reservations": [], "notes": ` + string(b) + `}`
}

// reject is a judge verdict rejecting with the given pointer→message pairs, in order.
func reject(pairs ...string) string {
	var sb strings.Builder
	sb.WriteString(`{"verdict": "reject", "feedback": {`)
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			sb.WriteString(", ")
		}
		k, _ := json.Marshal(pairs[i])
		v, _ := json.Marshal(pairs[i+1])
		sb.Write(k)
		sb.WriteString(": ")
		sb.Write(v)
	}
	sb.WriteString(`}}`)
	return sb.String()
}

// byRole filters calls to one role.
func byRole(calls []llmCall, role string) []llmCall {
	var out []llmCall
	for _, c := range calls {
		if c.Role == role {
			out = append(out, c)
		}
	}
	return out
}
