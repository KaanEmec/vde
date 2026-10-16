package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// parserLeniency names what parseJSON tolerates. It is hashed into service_version: change
// it whenever the leniency changes.
const parserLeniency = "answer-parser/1: first code fence, surrounding prose; no repair"

const fence = "```"

// parseJSON returns the JSON value in a model's answer with exactly the leniency of
// contract §6 step 3: surrounding whitespace and prose, and code fences, are stripped;
// the JSON itself is never repaired. The value must be an object, or an array when
// allowArray is set; with several values the first complete one is taken. The returned
// slice is the answer's own bytes. Errors say what was wrong and where, without content.
func parseJSON(text string, allowArray bool) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("the answer is empty")
	}
	if json.Valid([]byte(trimmed)) && allowedTop(trimmed[0], allowArray) {
		return json.RawMessage(trimmed), nil
	}

	body, base := text, 0
	if i := strings.Index(text, fence); i >= 0 {
		// The first fenced block: from the end of the opening fence's line (a language tag
		// may follow the fence) to the closing fence, or to the end when it is missing.
		start := i + len(fence)
		if nl := strings.IndexByte(text[start:], '\n'); nl >= 0 {
			start += nl + 1
		} else {
			start = len(text)
		}
		end := len(text)
		if j := strings.Index(text[start:], fence); j >= 0 {
			end = start + j
		}
		body, base = text[start:end], start
	}

	open := strings.IndexAny(body, "{[")
	if open < 0 {
		return nil, errors.New("no JSON value was found")
	}
	if !allowedTop(body[open], allowArray) {
		return nil, fmt.Errorf("the JSON value at offset %d is an array", base+open)
	}
	end, ok := balancedEnd(body, open)
	if !ok {
		return nil, fmt.Errorf("the JSON value starting at offset %d is incomplete", base+open)
	}
	value := body[open:end]
	var v any
	if err := json.Unmarshal([]byte(value), &v); err != nil {
		if se, ok := errors.AsType[*json.SyntaxError](err); ok {
			return nil, fmt.Errorf("invalid JSON at offset %d", int64(base+open)+se.Offset)
		}
		return nil, fmt.Errorf("invalid JSON starting at offset %d", base+open)
	}
	return json.RawMessage(value), nil
}

// allowedTop reports whether a value opening with c may be a whole answer.
func allowedTop(c byte, allowArray bool) bool {
	return c == '{' || (allowArray && c == '[')
}

// balancedEnd returns the end of the bracketed value opening at s[open], skipping
// brackets inside strings, and false when it never closes.
func balancedEnd(s string, open int) (int, bool) {
	depth, inString, escaped := 0, false, false
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case inString && c == '"':
			inString = false
		case inString:
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}
