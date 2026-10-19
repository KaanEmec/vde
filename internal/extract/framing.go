package extract

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kaanemec/vde/internal/provider"
)

// The fixed role framings (contract §6 steps 2 and 5), sent as the system prompt. Each
// file has one placeholder line filled per mode: with a schema or schema-less.
// docs/operator/framings.md documents them; their text is part of service_version.
var (
	//go:embed framings/extractor.md
	extractorFraming string
	//go:embed framings/judge.md
	judgeFraming string
)

// Mode-specific lines of the framings.
const (
	shapeSchema     = "Your answer conforms to the schema: one JSON object unless the schema's top level requires another type."
	shapeSchemaless = "No schema is given: shape the JSON exactly as the caller instructions describe, as one JSON object, or one array when they ask for a list."
	scopeSchema     = "The candidate has already been validated against the schema; judge what its values say, not its form."
	scopeSchemaless = "No schema is given: the caller instructions describe the expected JSON. Fields they ask for that the candidate lacks are completeness issues."
)

// Separators between the sections of a request, each at the start of a text part.
const (
	sepInstructions = "Caller instructions:"
	sepSchema       = "Schema:"
	sepDocument     = "Document:"
	sepPrevious     = "Previous candidate:"
	sepFeedback     = "Feedback:"
	sepCandidate    = "Candidate:"
)

// reaskReminder is appended to the judge request when its verdict could not be parsed.
const reaskReminder = `Reminder: your previous answer was not a valid verdict. Answer with exactly one JSON object, {"verdict": "accept" or "reject", "feedback": {"<JSON pointer>": "<message>"}}, optionally with "confidence"; a reject needs at least one feedback entry.`

// framings returns the system prompts for the extractor and the judge in the given mode.
func framings(schemaless bool) (extractor, judge string) {
	shape, scope := shapeSchema, scopeSchema
	if schemaless {
		shape, scope = shapeSchemaless, scopeSchemaless
	}
	return strings.Replace(extractorFraming, "{{shape}}", shape, 1),
		strings.Replace(judgeFraming, "{{scope}}", scope, 1)
}

// section is a text part opening with a separator. The content follows a blank line
// unchanged, so caller instructions reach the model byte for byte.
func section(sep, content string) provider.Part {
	return provider.Part{Text: sep + "\n\n" + content}
}

// assembleExtractor builds the extractor's parts in contract order: caller instructions,
// schema, document and, on a retry, the previous candidate and the feedback on it.
// prev is nil when the previous answer was not JSON or on the first attempt.
func assembleExtractor(in *Input, prev json.RawMessage, feedback []Feedback) []provider.Part {
	parts := commonParts(in.ExtractorInstructions, in)
	if prev != nil {
		parts = append(parts, section(sepPrevious, string(prev)))
	}
	if len(feedback) > 0 {
		parts = append(parts, section(sepFeedback, renderFeedback(feedback)))
	}
	return parts
}

// assembleJudge builds the judge's parts in contract order: caller instructions, schema,
// the same document parts the extractor saw, and the candidate. reask adds the format
// reminder used when the previous verdict could not be parsed.
func assembleJudge(in *Input, candidate json.RawMessage, reask bool) []provider.Part {
	parts := append(commonParts(in.JudgeInstructions, in), section(sepCandidate, string(candidate)))
	if reask {
		parts = append(parts, provider.Part{Text: reaskReminder})
	}
	return parts
}

func commonParts(instructions string, in *Input) []provider.Part {
	parts := make([]provider.Part, 0, len(in.Representation.Parts)+6)
	parts = append(parts, section(sepInstructions, instructions))
	if in.Schema != nil {
		parts = append(parts, section(sepSchema, string(in.Schema.raw)))
	}
	parts = append(parts, provider.Part{Text: sepDocument})
	return append(parts, in.Representation.Parts...)
}

// renderFeedback writes one line per entry: the quoted pointer, the source and the message.
func renderFeedback(feedback []Feedback) string {
	var b strings.Builder
	for i, f := range feedback {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- " + strconv.Quote(f.Pointer) + " (" + f.Source + "): " + f.Message)
	}
	return b.String()
}
