package extract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kaanemec/vde/internal/ground"
)

// verdict is a parsed judge answer (contract §7 plus the optional confidence of
// decision 14).
type verdict struct {
	accept     bool
	feedback   []Feedback // in the judge's order, source judge
	confidence *float64   // nil when absent or unusable
	// ignoredConfidence is set when a confidence was given but was not a number in [0, 1].
	ignoredConfidence bool
}

// parseVerdict parses the judge's answer: one JSON object (with the answer parser's
// leniency) whose verdict is exactly "accept" or "reject", whose feedback is an object of
// RFC 6901 pointers to string messages, kept in order, and whose optional confidence is a
// number from 0 to 1. Unknown keys are ignored. A reject without entries is unparseable.
// Messages are capped at maxChars characters and otherwise kept as given.
func parseVerdict(text string, maxChars int) (verdict, error) {
	raw, err := parseJSON(text, false)
	if err != nil {
		return verdict{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil { // the opening brace, checked by parseJSON
		return verdict{}, fmt.Errorf("verdict: %w", err)
	}
	var v verdict
	var word string
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return verdict{}, fmt.Errorf("verdict: %w", err)
		}
		key, _ := tok.(string) // object keys are always strings
		if seen[key] && (key == "verdict" || key == "feedback") {
			return verdict{}, fmt.Errorf("verdict: duplicate key %q", key)
		}
		seen[key] = true
		switch key {
		case "verdict":
			if err := dec.Decode(&word); err != nil {
				return verdict{}, errors.New("verdict: verdict is not a string")
			}
		case "feedback":
			if v.feedback, err = decodeFeedback(dec, maxChars); err != nil {
				return verdict{}, err
			}
		case "confidence":
			var c json.RawMessage
			if err := dec.Decode(&c); err != nil {
				return verdict{}, fmt.Errorf("verdict: %w", err)
			}
			var f float64
			if json.Unmarshal(c, &f) == nil && f >= 0 && f <= 1 {
				v.confidence = &f
			} else {
				v.ignoredConfidence = true
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return verdict{}, fmt.Errorf("verdict: %w", err)
			}
		}
	}
	switch word {
	case "accept":
		v.accept = true
	case "reject":
		if len(v.feedback) == 0 {
			return verdict{}, errors.New("verdict: reject without feedback")
		}
	default:
		return verdict{}, errors.New("verdict: verdict is not accept or reject")
	}
	return v, nil
}

// decodeFeedback reads the feedback object with a token walk, keeping the judge's order.
func decodeFeedback(dec *json.Decoder, maxChars int) ([]Feedback, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("verdict: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("verdict: feedback is not an object")
	}
	out := []Feedback{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("verdict: %w", err)
		}
		ptr, _ := tok.(string) // object keys are always strings
		if !ground.ValidPointer(ptr) {
			return nil, errors.New("verdict: feedback key is not a JSON pointer")
		}
		var value json.RawMessage
		var msg string
		if dec.Decode(&value) != nil || value[0] != '"' || json.Unmarshal(value, &msg) != nil {
			return nil, errors.New("verdict: feedback message is not a string")
		}
		out = append(out, Feedback{Pointer: ptr, Message: capMessage(msg, maxChars), Source: SourceJudge})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, fmt.Errorf("verdict: %w", err)
	}
	return out, nil
}

// capMessage truncates msg to maxChars characters, the last being an ellipsis.
func capMessage(msg string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(msg) <= maxChars {
		return msg
	}
	var b strings.Builder
	n := 0
	for _, r := range msg {
		if n == maxChars-1 {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString("…")
	return b.String()
}
