package bedrock

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	secretText = "SECRET-DOCUMENT-TEXT"
	secretBlob = "SECRET-BLOB-BYTES"
	secretAns  = "SECRET-ANSWER-TEXT"
)

// fake is a ConverseAPI that records the input and replies with out or err.
type fake struct {
	in  *bedrockruntime.ConverseInput
	out *bedrockruntime.ConverseOutput
	err error
}

func (f *fake) Converse(_ context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	f.in = in
	return f.out, f.err
}

func answer(text string, stop types.StopReason) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role:    types.ConversationRoleAssistant,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
		}},
		StopReason: stop,
		Usage:      &types.TokenUsage{InputTokens: aws.Int32(11), OutputTokens: aws.Int32(7)},
	}
}

func TestComplete_requestMapping(t *testing.T) {
	t.Parallel()
	f := &fake{out: answer(secretAns, types.StopReasonEndTurn)}
	c := &Client{api: f}
	resp, err := c.Complete(context.Background(), provider.Request{
		Model:     "anthropic.example-model",
		System:    "You are the extractor.",
		MaxTokens: 1234,
		Parts: []provider.Part{
			{Text: secretText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte(secretBlob)}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte(secretBlob)}},
			{Document: &provider.Blob{MediaType: "text/plain", Data: []byte("x")}},
		},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text != secretAns || resp.InputTokens != 11 || resp.OutputTokens != 7 || resp.Truncated {
		t.Errorf("response = %+v", resp)
	}

	in := f.in
	if aws.ToString(in.ModelId) != "anthropic.example-model" {
		t.Errorf("ModelId = %q", aws.ToString(in.ModelId))
	}
	if len(in.System) != 1 || in.System[0].(*types.SystemContentBlockMemberText).Value != "You are the extractor." {
		t.Errorf("System = %+v", in.System)
	}
	if aws.ToInt32(in.InferenceConfig.MaxTokens) != 1234 || in.InferenceConfig.Temperature != nil {
		t.Errorf("InferenceConfig = %+v", in.InferenceConfig)
	}
	if len(in.Messages) != 1 || in.Messages[0].Role != types.ConversationRoleUser || len(in.Messages[0].Content) != 4 {
		t.Fatalf("Messages = %+v", in.Messages)
	}
	blocks := in.Messages[0].Content
	if blocks[0].(*types.ContentBlockMemberText).Value != secretText {
		t.Errorf("block 0 = %+v", blocks[0])
	}
	img := blocks[1].(*types.ContentBlockMemberImage).Value
	if img.Format != types.ImageFormatPng || string(img.Source.(*types.ImageSourceMemberBytes).Value) != secretBlob {
		t.Errorf("image block = %+v", img)
	}
	pdf := blocks[2].(*types.ContentBlockMemberDocument).Value
	if pdf.Format != types.DocumentFormatPdf || string(pdf.Source.(*types.DocumentSourceMemberBytes).Value) != secretBlob {
		t.Errorf("pdf block = %+v", pdf)
	}
	txt := blocks[3].(*types.ContentBlockMemberDocument).Value
	if txt.Format != types.DocumentFormatTxt || aws.ToString(pdf.Name) == aws.ToString(txt.Name) {
		t.Errorf("text document block = %+v (names must be distinct)", txt)
	}
}

func TestComplete_defaults(t *testing.T) {
	t.Parallel()
	f := &fake{out: answer("ok", types.StopReasonEndTurn)}
	_, err := (&Client{api: f}).Complete(context.Background(), provider.Request{Model: "m", MaxTokens: 4096, Parts: []provider.Part{{Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToInt32(f.in.InferenceConfig.MaxTokens) != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", aws.ToInt32(f.in.InferenceConfig.MaxTokens))
	}
	if f.in.InferenceConfig.Temperature != nil || len(f.in.System) != 0 {
		t.Errorf("temperature or system sent without being set: %+v", f.in)
	}
}

func TestComplete_truncated(t *testing.T) {
	t.Parallel()
	f := &fake{out: answer("cut off", types.StopReasonMaxTokens)}
	resp, err := (&Client{api: f}).Complete(context.Background(), provider.Request{Model: "m", Parts: []provider.Part{{Text: "hi"}}})
	if err != nil || !resp.Truncated {
		t.Errorf("Complete = %+v, %v; want Truncated", resp, err)
	}
}

func TestComplete_badInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  provider.Request
	}{
		{"no model", provider.Request{Parts: []provider.Part{{Text: "x"}}}},
		{"no parts", provider.Request{Model: "m"}},
		{"empty part", provider.Request{Model: "m", Parts: []provider.Part{{}}}},
		{"two fields", provider.Request{Model: "m", Parts: []provider.Part{{Text: "x", Image: &provider.Blob{MediaType: "image/png", Data: []byte("x")}}}}},
		{"heic image", provider.Request{Model: "m", Parts: []provider.Part{{Image: &provider.Blob{MediaType: "image/heic", Data: []byte(secretBlob)}}}}},
		{"unsupported document", provider.Request{Model: "m", Parts: []provider.Part{{Document: &provider.Blob{MediaType: "application/zip", Data: []byte(secretBlob)}}}}},
		{"empty document", provider.Request{Model: "m", Parts: []provider.Part{{Document: &provider.Blob{MediaType: "application/pdf"}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fake{}
			_, err := (&Client{api: f}).Complete(context.Background(), tt.req)
			if err == nil || provider.Classify(err) != provider.Permanent {
				t.Fatalf("error = %v, want a permanent error", err)
			}
			if f.in != nil {
				t.Error("the API was called for an invalid request")
			}
			if strings.Contains(err.Error(), secretBlob) {
				t.Errorf("error contains content: %v", err)
			}
		})
	}
}

func TestComplete_unusableAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		out  *bedrockruntime.ConverseOutput
	}{
		{"guardrail", answer(secretAns, types.StopReasonGuardrailIntervened)},
		{"content filtered", answer(secretAns, types.StopReasonContentFiltered)},
		{"no text", answer("  ", types.StopReasonEndTurn)},
		{"no message", &bedrockruntime.ConverseOutput{StopReason: types.StopReasonEndTurn}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Client{api: &fake{out: tt.out}}).Complete(context.Background(), provider.Request{Model: "m", Parts: []provider.Part{{Text: "x"}}})
			if err == nil || provider.Classify(err) != provider.Permanent {
				t.Fatalf("error = %v, want permanent", err)
			}
			if strings.Contains(err.Error(), secretAns) {
				t.Errorf("error contains the answer: %v", err)
			}
		})
	}
}

func httpStatus(status int) error {
	return &awshttp.ResponseError{ResponseError: &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      errors.New("boom " + secretText),
	}}
}

func TestComplete_errorClassification(t *testing.T) {
	t.Parallel()
	leak := aws.String("message quoting " + secretText)
	tests := []struct {
		name string
		err  error
		want provider.Kind
	}{
		{"throttling", &types.ThrottlingException{Message: leak}, provider.Retryable},
		{"service quota", &types.ServiceQuotaExceededException{Message: leak}, provider.Retryable},
		{"model timeout", &types.ModelTimeoutException{Message: leak}, provider.Retryable},
		{"model not ready", &types.ModelNotReadyException{Message: leak}, provider.Retryable},
		{"internal server", &types.InternalServerException{Message: leak}, provider.Retryable},
		{"service unavailable", &types.ServiceUnavailableException{Message: leak}, provider.Retryable},
		{"wrapped throttling", &smithy.OperationError{ServiceID: "Bedrock Runtime", OperationName: "Converse", Err: &types.ThrottlingException{Message: leak}}, provider.Retryable},
		{"access denied", &types.AccessDeniedException{Message: leak}, provider.Permanent},
		{"validation", &types.ValidationException{Message: leak}, provider.Permanent},
		{"http 503 without a typed error", httpStatus(503), provider.Retryable},
		{"http 429 without a typed error", httpStatus(429), provider.Retryable},
		{"http 403 without a typed error", httpStatus(403), provider.Permanent},
		{"unknown api error", &smithy.GenericAPIError{Code: "SomethingNew", Message: secretText}, provider.Permanent},
		{"deadline", context.DeadlineExceeded, provider.Retryable},
		{"cancelled", context.Canceled, provider.Permanent},
		{"other", errors.New("who knows"), provider.Permanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Client{api: &fake{err: tt.err}}).Complete(context.Background(), provider.Request{Model: "m", Parts: []provider.Part{{Text: "x"}}})
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("error = %v, want *provider.Error", err)
			}
			if pe.Kind != tt.want {
				t.Errorf("Kind = %v, want %v (%v)", pe.Kind, tt.want, err)
			}
			if strings.Contains(err.Error(), secretText) {
				t.Errorf("error contains service message text: %v", err)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "example")
	if _, err := New(&config.Endpoint{}); err == nil {
		t.Error("New without a region succeeded")
	}
	c, err := New(&config.Endpoint{Region: "us-east-1"})
	if err != nil || c == nil || c.api == nil {
		t.Errorf("New = %v, %v", c, err)
	}
}

// TestComplete_throughSDK drives the real SDK client against a local server: the path,
// the SigV4 signature from the default credential chain and the response decoding.
func TestComplete_throughSDK(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "example")
	t.Setenv("AWS_SESSION_TOKEN", "")
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.EscapedPath(), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output":{"message":{"role":"assistant","content":[{"text":"hello"}]}},`+
			`"stopReason":"end_turn","usage":{"inputTokens":3,"outputTokens":2,"totalTokens":5}}`)
	}))
	defer srv.Close()

	c, err := New(&config.Endpoint{Region: "eu-west-1", BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Complete(context.Background(), provider.Request{Model: "anthropic.example-model", Parts: []provider.Part{{Text: "hi"}}})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text != "hello" || resp.InputTokens != 3 || resp.OutputTokens != 2 {
		t.Errorf("response = %+v", resp)
	}
	if path != "/model/anthropic.example-model/converse" {
		t.Errorf("path = %q", path)
	}
	if !strings.Contains(auth, "AWS4-HMAC-SHA256") || !strings.Contains(auth, "/eu-west-1/bedrock/") {
		t.Errorf("Authorization = %q, want a SigV4 signature for eu-west-1", auth)
	}
}
