package vertex

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/anthropic"
	"github.com/kaanemec/vde/internal/provider/gemini"
)

const (
	cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

	// anthropicVersion is the body field Vertex requires in place of the anthropic-version
	// header and the model name (the model is in the URL).
	anthropicVersion = "vertex-2023-10-16"
)

// Client calls Gemini and Anthropic models on Vertex AI. It is safe for concurrent use.
type Client struct {
	baseURL string // https://{region}-aiplatform.googleapis.com unless overridden
	project string
	region  string
	tokens  oauth2.TokenSource
	http    *http.Client
}

var _ provider.Provider = (*Client)(nil)

// New returns a Client for the endpoint. It finds Google application default credentials
// (GOOGLE_APPLICATION_CREDENTIALS, the gcloud user credentials, or the metadata server of
// a Google Cloud runtime) and fails when there are none. The token is refreshed as
// needed and sent only in the Authorization header.
func New(ep *config.Endpoint) (*Client, error) {
	creds, err := google.FindDefaultCredentials(context.Background(), cloudPlatformScope)
	if err != nil {
		return nil, &provider.Error{Message: "vertex: no Google application default credentials found", Err: err}
	}
	return newClient(ep, creds.TokenSource), nil
}

func newClient(ep *config.Endpoint, tokens oauth2.TokenSource) *Client {
	base := strings.TrimRight(ep.BaseURL, "/")
	if base == "" {
		base = "https://" + hostPrefix(ep.Region) + "aiplatform.googleapis.com"
	}
	return &Client{
		baseURL: base,
		project: ep.Project,
		region:  ep.Region,
		tokens:  tokens,
		http:    provider.NewHTTPClient(ep.Timeout, provider.DefaultMaxResponseBytes),
	}
}

// hostPrefix is "{region}-" for a regional endpoint and empty for the global one.
func hostPrefix(region string) string {
	if region == "global" {
		return ""
	}
	return region + "-"
}

// Complete runs one call. Models named claude* go to the Anthropic publisher, gemini* to
// the Google publisher, with the bodies and answers of the anthropic and gemini adapters;
// anything else is rejected. Errors never contain request or response content.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Model == "" {
		return provider.Response{}, &provider.Error{Message: "vertex: request has no model"}
	}
	if len(req.Parts) == 0 {
		return provider.Response{}, &provider.Error{Message: "vertex: request has no content parts"}
	}
	switch {
	case strings.HasPrefix(req.Model, "claude"):
		body, err := anthropic.Body(req, anthropicVersion)
		if err != nil {
			return provider.Response{}, err
		}
		raw, _, err := c.post(ctx, c.modelURL("anthropic", req.Model, "rawPredict"), body)
		if err != nil {
			return provider.Response{}, err
		}
		return anthropic.Parse(raw)
	case strings.HasPrefix(req.Model, "gemini"):
		body, err := gemini.Body(req)
		if err != nil {
			return provider.Response{}, err
		}
		raw, status, err := c.post(ctx, c.modelURL("google", req.Model, "generateContent"), body)
		if err != nil {
			return provider.Response{}, err
		}
		return gemini.Parse(raw, status)
	default:
		return provider.Response{}, &provider.Error{Message: "vertex: model must be a gemini or claude model"}
	}
}

func (c *Client) modelURL(publisher, model, method string) string {
	return c.baseURL + "/v1/projects/" + url.PathEscape(c.project) +
		"/locations/" + url.PathEscape(c.region) +
		"/publishers/" + publisher + "/models/" + url.PathEscape(model) + ":" + method
}

// post sends the body with a bearer token and returns the body of a 2xx response.
func (c *Client) post(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	tok, err := c.tokens.Token()
	if err != nil {
		return nil, 0, &provider.Error{
			Kind:    provider.Classify(err),
			Message: "vertex: obtaining an access token failed",
			Err:     err,
		}
	}
	header := http.Header{}
	header.Set("Authorization", tok.Type()+" "+tok.AccessToken)
	return provider.PostJSON(ctx, c.http, endpoint, header, body, "vertex")
}
