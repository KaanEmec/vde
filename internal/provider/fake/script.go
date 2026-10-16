package fake

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kaanemec/vde/internal/provider"
)

// Judge verdicts used by the script helpers (contract §7).
const (
	AcceptVerdict = `{"verdict": "accept", "feedback": {}}`
	RejectVerdict = `{"verdict": "reject", "feedback": {"": "the candidate does not match the document"}}`
)

// The helpers below build ordered scripts for one fake serving both roles, which the
// sequential extraction loop calls extractor first, then judge.

// AcceptFirstTry: the extractor answers candidate and the judge accepts it.
func AcceptFirstTry(candidate string) Script {
	return Script{Answer(candidate), Answer(AcceptVerdict)}
}

// SchemaInvalidThenValid: the extractor answers invalid (no judge call follows), then
// valid, which the judge accepts.
func SchemaInvalidThenValid(invalid, valid string) Script {
	return Script{Answer(invalid), Answer(valid), Answer(AcceptVerdict)}
}

// RejectThenAccept: the judge rejects first and accepts second.
func RejectThenAccept(first, second string) Script {
	return Script{Answer(first), Answer(RejectVerdict), Answer(second), Answer(AcceptVerdict)}
}

// AlwaysRetryable fails every call retryably with status.
func AlwaysRetryable(status int) Script {
	s := Fail(provider.Retryable, status)
	s.Repeat = true
	return Script{s}
}

// AlwaysPermanent fails every call permanently with status.
func AlwaysPermanent(status int) Script {
	s := Fail(provider.Permanent, status)
	s.Repeat = true
	return Script{s}
}

// Load reads a script file (see the package documentation) and returns a provider
// replaying it.
func Load(path string) (*Provider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fake: %w", err)
	}
	script, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return New(script), nil
}

type fileScript struct {
	Description string     `yaml:"description"`
	Steps       []fileStep `yaml:"steps"`
}

type fileStep struct {
	Role   string        `yaml:"role"`
	Model  string        `yaml:"model"`
	Delay  time.Duration `yaml:"delay"`
	Answer *string       `yaml:"answer"`
	Error  *fileError    `yaml:"error"`
	Repeat bool          `yaml:"repeat"`
}

type fileError struct {
	Kind       string        `yaml:"kind"`
	Status     int           `yaml:"status"`
	RetryAfter time.Duration `yaml:"retry_after"`
}

// Parse decodes a YAML script, rejecting unknown keys and malformed steps.
func Parse(data []byte) (Script, error) {
	var f fileScript
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("fake: script is empty")
		}
		return nil, fmt.Errorf("fake: %w", err)
	}
	if len(f.Steps) == 0 {
		return nil, errors.New("fake: script has no steps")
	}
	script := make(Script, len(f.Steps))
	for i, fs := range f.Steps {
		s := Step{Role: fs.Role, Model: fs.Model, Delay: fs.Delay, Repeat: fs.Repeat}
		switch {
		case (fs.Answer == nil) == (fs.Error == nil):
			return nil, fmt.Errorf("fake: steps[%d]: exactly one of answer or error is required", i)
		case fs.Delay < 0:
			return nil, fmt.Errorf("fake: steps[%d].delay: must not be negative", i)
		case fs.Answer != nil:
			s.Answer = *fs.Answer
		default:
			var kind provider.Kind
			switch fs.Error.Kind {
			case "retryable":
				kind = provider.Retryable
			case "permanent":
				kind = provider.Permanent
			default:
				return nil, fmt.Errorf("fake: steps[%d].error.kind: %q is not retryable or permanent", i, fs.Error.Kind)
			}
			s.Err = &provider.Error{
				Kind:       kind,
				Status:     fs.Error.Status,
				Message:    "fake: scripted failure",
				RetryAfter: fs.Error.RetryAfter,
			}
		}
		script[i] = s
	}
	return script, nil
}
