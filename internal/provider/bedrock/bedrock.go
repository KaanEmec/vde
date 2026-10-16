package bedrock

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const defaultMaxTokens = 4096

// ConverseAPI is the part of the Bedrock runtime client the adapter uses. Tests inject a
// fake; *bedrockruntime.Client satisfies it.
type ConverseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// Client calls the Bedrock Converse API. It is safe for concurrent use.
type Client struct {
	api ConverseAPI
}

var _ provider.Provider = (*Client)(nil)

// New returns a Client for the endpoint. The region is ep.Region; credentials are
// resolved lazily, on the first call, through the AWS default chain. A base_url, when
// set, replaces the service endpoint (a VPC endpoint or a test server). The SDK's own
// retries are off: failover and retry belong to the router, which sees the Kind.
func New(ep *config.Endpoint) (*Client, error) {
	if ep.Region == "" {
		return nil, errors.New("bedrock: endpoint has no region")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(ep.Region),
		awsconfig.WithHTTPClient(provider.NewHTTPClient(ep.Timeout, provider.DefaultMaxResponseBytes)),
		awsconfig.WithRetryMaxAttempts(1),
	)
	if err != nil {
		return nil, fmt.Errorf("bedrock: loading AWS configuration: %w", err)
	}
	client := bedrockruntime.NewFromConfig(cfg, func(o *bedrockruntime.Options) {
		if ep.BaseURL != "" {
			o.BaseEndpoint = aws.String(ep.BaseURL)
		}
	})
	return &Client{api: client}, nil
}

// Complete runs one Converse call. Errors never contain request or response content: for
// service errors only the error code is reported.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	in, err := buildInput(req)
	if err != nil {
		return provider.Response{}, err
	}
	out, err := c.api.Converse(ctx, in)
	if err != nil {
		return provider.Response{}, classify(err)
	}
	return toResponse(out, req.Model)
}

func buildInput(req provider.Request) (*bedrockruntime.ConverseInput, error) {
	if req.Model == "" {
		return nil, &provider.Error{Message: "bedrock: request has no model"}
	}
	if len(req.Parts) == 0 {
		return nil, &provider.Error{Message: "bedrock: request has no content parts"}
	}
	blocks := make([]types.ContentBlock, 0, len(req.Parts))
	docs := 0
	for _, p := range req.Parts {
		b, err := block(p, &docs)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	maxTokens := int32(req.MaxTokens)
	if req.MaxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	cfg := &types.InferenceConfiguration{MaxTokens: aws.Int32(maxTokens)}
	if req.Temperature > 0 { // zero means "leave it to the model"
		cfg.Temperature = aws.Float32(float32(req.Temperature))
	}
	in := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(req.Model),
		Messages:        []types.Message{{Role: types.ConversationRoleUser, Content: blocks}},
		InferenceConfig: cfg,
	}
	if req.System != "" {
		in.System = []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}}
	}
	return in, nil
}

var (
	imageFormats = map[string]types.ImageFormat{
		"image/png":  types.ImageFormatPng,
		"image/jpeg": types.ImageFormatJpeg,
		"image/webp": types.ImageFormatWebp,
	}
	documentFormats = map[string]types.DocumentFormat{
		"application/pdf": types.DocumentFormatPdf,
		"text/plain":      types.DocumentFormatTxt,
		"text/html":       types.DocumentFormatHtml,
		"text/markdown":   types.DocumentFormatMd,
	}
)

// block maps one part to a content block. Exactly one field of a Part must be set.
func block(p provider.Part, docs *int) (types.ContentBlock, error) {
	switch {
	case p.Text != "" && p.Image == nil && p.Document == nil:
		return &types.ContentBlockMemberText{Value: p.Text}, nil
	case p.Image != nil && p.Text == "" && p.Document == nil:
		f, ok := imageFormats[p.Image.MediaType]
		if !ok || len(p.Image.Data) == 0 {
			return nil, &provider.Error{Message: "bedrock: image part is empty or its media type is not png, jpeg or webp"}
		}
		return &types.ContentBlockMemberImage{Value: types.ImageBlock{
			Format: f,
			Source: &types.ImageSourceMemberBytes{Value: p.Image.Data},
		}}, nil
	case p.Document != nil && p.Text == "" && p.Image == nil:
		f, ok := documentFormats[p.Document.MediaType]
		if !ok || len(p.Document.Data) == 0 {
			return nil, &provider.Error{Message: "bedrock: document part is empty or its media type is not supported"}
		}
		*docs++
		// The name is generic on purpose: Bedrock puts it in the prompt, and a file name
		// could carry content or instructions.
		return &types.ContentBlockMemberDocument{Value: types.DocumentBlock{
			Format: f,
			Name:   aws.String(fmt.Sprintf("document %d", *docs)),
			Source: &types.DocumentSourceMemberBytes{Value: p.Document.Data},
		}}, nil
	default:
		return nil, &provider.Error{Message: "bedrock: part must set exactly one of text, image or document"}
	}
}

func toResponse(out *bedrockruntime.ConverseOutput, model string) (provider.Response, error) {
	switch out.StopReason {
	case types.StopReasonGuardrailIntervened, types.StopReasonContentFiltered:
		return provider.Response{}, &provider.Error{Message: "bedrock: answer blocked by the provider"}
	}
	msg, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return provider.Response{}, &provider.Error{Message: "bedrock: response had no message"}
	}
	var text strings.Builder
	for _, b := range msg.Value.Content {
		if t, ok := b.(*types.ContentBlockMemberText); ok {
			text.WriteString(t.Value)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return provider.Response{}, &provider.Error{Message: "bedrock: the model returned no text"}
	}
	resp := provider.Response{
		Text:      text.String(),
		Model:     model,
		Truncated: out.StopReason == types.StopReasonMaxTokens,
	}
	if out.Usage != nil {
		resp.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		resp.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
	}
	return resp, nil
}

// classify turns an SDK error into a *provider.Error. Throttling, quota, timeout, 5xx
// and network failures are Retryable; validation, access and everything else Permanent.
// Service errors carry only their code: the SDK message can quote the request.
func classify(err error) *provider.Error {
	var (
		throttle *types.ThrottlingException
		quota    *types.ServiceQuotaExceededException
		timeout  *types.ModelTimeoutException
		notReady *types.ModelNotReadyException
		internal *types.InternalServerException
		unavail  *types.ServiceUnavailableException
		denied   *types.AccessDeniedException
		invalid  *types.ValidationException
	)
	switch {
	case errors.As(err, &throttle), errors.As(err, &quota):
		return serviceError(err, provider.Retryable, "throttled")
	case errors.As(err, &timeout), errors.As(err, &notReady), errors.As(err, &internal), errors.As(err, &unavail):
		return serviceError(err, provider.Retryable, "service unavailable")
	case errors.As(err, &denied):
		return serviceError(err, provider.Permanent, "access denied")
	case errors.As(err, &invalid):
		return serviceError(err, provider.Permanent, "request rejected")
	}
	var re *awshttp.ResponseError
	if errors.As(err, &re) {
		status := re.HTTPStatusCode()
		kind := provider.Permanent
		if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
			kind = provider.Retryable
		}
		return &provider.Error{Kind: kind, Status: status, Message: "bedrock: " + code(err, "request rejected")}
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		return &provider.Error{Message: "bedrock: " + api.ErrorCode()}
	}
	// Network, deadline or credential failures carry no request content.
	return &provider.Error{Kind: provider.Classify(err), Message: "bedrock: request failed", Err: err}
}

func serviceError(err error, kind provider.Kind, what string) *provider.Error {
	return &provider.Error{Kind: kind, Message: "bedrock: " + what + " (" + code(err, "unknown") + ")"}
}

func code(err error, fallback string) string {
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() != "" {
		return api.ErrorCode()
	}
	return fallback
}
