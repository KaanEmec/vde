package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// Path is the route posted to, under the endpoint's base_url.
const Path = "/classify"

// WholeResultMessage opens the single feedback entry of a reject, at pointer "". It is
// shown to end users, so it says what to do without naming the questions.
const WholeResultMessage = "An automated check found that this extraction does not match the document. " +
	"Check every value against the document and correct what differs."

var _ provider.Provider = (*Client)(nil)

// Client calls the classify route of one classifier endpoint.
type Client struct {
	http      *http.Client
	url       string // {base_url}/classify
	key       string // empty for a proxy without authentication
	questions map[string][]config.Question
}

// New returns a client for ep, whose models carry their questions.
func New(ep *config.Endpoint) *Client {
	qs := make(map[string][]config.Question, len(ep.Models))
	for name, m := range ep.Models {
		qs[name] = m.Questions
	}
	return &Client{
		http:      provider.NewHTTPClient(ep.Timeout, provider.DefaultMaxResponseBytes),
		url:       strings.TrimRight(ep.BaseURL, "/") + Path,
		key:       ep.APIKey(),
		questions: qs,
	}
}

// Request is the body of POST {base_url}/classify.
type Request struct {
	Model     string     `json:"model"`
	Questions []Question `json:"questions"`
	Text      string     `json:"text"`
}

// Question is one question as sent: its id and its text.
type Question struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Response is the answer of the classify route.
type Response struct {
	Decisions []Decision `json:"decisions"`
}

// Decision answers one question. Probability is the calibrated probability of "yes"
// (the candidate passes); Label is the classifier's own label, informational.
type Decision struct {
	Question    string   `json:"question"`
	Label       string   `json:"label"`
	Probability *float64 `json:"probability"`
}

// Complete sends the text parts of req, joined by blank lines, with the questions of
// req.Model, and answers with the verdict JSON of the judge role. The system prompt (the
// judge framing) is not sent: the questions replace it.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	questions := c.questions[req.Model]
	if len(questions) == 0 {
		return provider.Response{}, &provider.Error{Message: "classifier model has no questions"}
	}
	texts := make([]string, 0, len(req.Parts))
	for _, p := range req.Parts {
		if p.Image != nil || p.Document != nil {
			return provider.Response{}, &provider.Error{Message: "a classifier reads text only"}
		}
		texts = append(texts, p.Text)
	}
	body := Request{Model: req.Model, Text: strings.Join(texts, "\n\n")}
	for _, q := range questions {
		body.Questions = append(body.Questions, Question{ID: q.ID, Text: q.Text})
	}
	data, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, &provider.Error{Message: "encoding request failed", Err: err}
	}
	header := http.Header{}
	header.Set("Accept", "application/json")
	if c.key != "" {
		header.Set("Authorization", "Bearer "+c.key)
	}
	raw, _, err := provider.PostJSON(ctx, c.http, c.url, header, data, "classifier")
	if err != nil {
		return provider.Response{}, err
	}
	var cr Response
	if json.Unmarshal(raw, &cr) != nil {
		// Not wrapped: json errors can quote a byte of the response.
		return provider.Response{}, &provider.Error{Message: "malformed response"}
	}
	text, err := Verdict(questions, cr.Decisions)
	if err != nil {
		return provider.Response{}, err
	}
	return provider.Response{Text: text}, nil
}

// verdictJSON is the judge verdict of contract §7 plus the optional confidence.
type verdictJSON struct {
	Verdict    string            `json:"verdict"`
	Feedback   map[string]string `json:"feedback"`
	Confidence float64           `json:"confidence"`
}

// Verdict renders decisions as the judge's verdict JSON: accept when every question's
// probability is at least its threshold, confidence the minimum probability, and on
// reject one entry at pointer "" with WholeResultMessage and the confidence. Every
// question needs exactly one decision with a probability from 0 to 1; decisions for
// unknown questions are ignored.
func Verdict(questions []config.Question, decisions []Decision) (string, error) {
	byID := make(map[string]float64, len(decisions))
	for _, d := range decisions {
		if _, dup := byID[d.Question]; dup {
			return "", &provider.Error{Message: "duplicate decision for one question"}
		}
		if d.Probability == nil || *d.Probability < 0 || *d.Probability > 1 {
			return "", &provider.Error{Message: "decision without a probability from 0 to 1"}
		}
		byID[d.Question] = *d.Probability
	}
	v := verdictJSON{Verdict: "accept", Feedback: map[string]string{}, Confidence: 1}
	for _, q := range questions {
		p, ok := byID[q.ID]
		if !ok {
			return "", &provider.Error{Message: "no decision for question " + q.ID}
		}
		v.Confidence = min(v.Confidence, p)
		if p < q.Threshold {
			v.Verdict = "reject"
		}
	}
	if v.Verdict == "reject" {
		v.Feedback[""] = fmt.Sprintf("%s (Classifier confidence %.2f.)", WholeResultMessage, v.Confidence)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return "", &provider.Error{Message: "encoding verdict failed", Err: err}
	}
	return string(out), nil
}
