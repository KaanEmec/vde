package config

import (
	"strings"
	"testing"
)

const cloudYAML = `
server: {listen: ":8090"}
endpoints:
  bedrock:
    provider: bedrock
    region: us-east-1
    models:
      anthropic.example-model: {accepts: [application/pdf, image/png, text/plain]}
  vertex:
    provider: vertex
    project: my-project
    region: us-east5
    models:
      example-model: {accepts: [application/pdf, image/png, text/plain]}
groups:
  main:
    endpoints:
      - {endpoint: bedrock, model: anthropic.example-model}
      - {endpoint: vertex, model: example-model}
roles:
  extractor: {group: main}
  judge: {group: main}
applications:
  - {id: travelinbox}
`

func parseCloud(t *testing.T, edit func(c *Config)) Issues {
	t.Helper()
	cfg, err := Parse([]byte(cloudYAML), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	edit(cfg)
	return cfg.Validate()
}

func TestValidate_cloudProviders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		edit    func(c *Config)
		wantKey string // "" means no error
		wantMsg string
	}{
		{name: "valid without key or base_url", edit: func(c *Config) {}},
		{
			name:    "bedrock needs a region",
			edit:    func(c *Config) { c.Endpoints["bedrock"].Region = "" },
			wantKey: "endpoints.bedrock.region", wantMsg: "is required",
		},
		{
			name:    "vertex needs a region",
			edit:    func(c *Config) { c.Endpoints["vertex"].Region = "" },
			wantKey: "endpoints.vertex.region", wantMsg: "is required",
		},
		{
			name:    "vertex needs a project",
			edit:    func(c *Config) { c.Endpoints["vertex"].Project = "" },
			wantKey: "endpoints.vertex.project", wantMsg: "is required",
		},
		{
			name:    "a key variable is rejected",
			edit:    func(c *Config) { c.Endpoints["bedrock"].APIKeyEnv = "AWS_SECRET" },
			wantKey: "endpoints.bedrock.api_key_env", wantMsg: "default credential chain",
		},
		{
			name: "region on another provider",
			edit: func(c *Config) {
				e := c.Endpoints["bedrock"]
				e.Provider, e.Region, e.BaseURL, e.APIKeyEnv = "gemini", "us-east-1", "https://example.com", "X"
			},
			wantKey: "endpoints.bedrock.region", wantMsg: "applies only to",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var found bool
			for _, is := range parseCloud(t, tt.edit) {
				if is.Warning {
					continue
				}
				if tt.wantKey == "" {
					t.Errorf("unexpected error %v", is)
					continue
				}
				if is.Key == tt.wantKey && strings.Contains(is.Message, tt.wantMsg) {
					found = true
				}
			}
			if tt.wantKey != "" && !found {
				t.Errorf("no error for %s containing %q", tt.wantKey, tt.wantMsg)
			}
		})
	}
}
