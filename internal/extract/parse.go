package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// parserLeniency names what parseJSON tolerates. It is hashed into service_version: change
// it whenever the leniency changes.
const parserLeniency = "answer-parser/2: the one closed <json> block, after a closed <analysis> when present; " +
	"else first code fence, surrounding prose; no repair"

const fence = "```"

// The tags of the answer blocks (ADR 0012): the analysis, then the answer.
const (
	tagAnalysis = "analysis"
	tagJSON     = "json"
)

// parseJSON returns the JSON value in the extractor's answer. An answer in blocks (ADR
// 0012) is read from its <json> block only; a bare answer as contract §6 step 3 allows.
// See parseBlock.
func parseJSON(text string, allowArray bool) (json.RawMessage, error) {
	return parseBlock(text, tagJSON, allowArray)
}

// parseBlock returns the JSON value in a model's answer. When the answer has an
// <analysis> block or a <tag> block, the value is read from the <tag> block only: the
// analysis must be closed, the <tag> block must follow it, be closed and be the only one
// after it; anything else is a parse failure, so a value in the analysis is never taken.
// Without either block the whole answer is read. Within what is read, the leniency of
// contract §6 step 3 applies (parseValue). The analysis is never returned.
func parseBlock(text, tag string, allowArray bool) (json.RawMessage, error) {
	content, base, ok, err := block(text, tag)
	if err != nil {
		return nil, err
	}
	if !ok {
		return parseValue(text, 0, allowArray)
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("the <%s> block is empty", tag)
	}
	return parseValue(content, base, allowArray)
}

// block returns the content of the answer's <tag> block and its offset in text, ok false
// when the answer has neither an analysis nor a <tag> block. Errors name the tags, never
// the content.
func block(text, tag string) (content string, base int, ok bool, err error) {
	rest := text
	analysis := strings.Index(text, "<"+tagAnalysis+">")
	if analysis >= 0 {
		end := strings.Index(text[analysis:], "</"+tagAnalysis+">")
		if end < 0 {
			return "", 0, false, fmt.Errorf("the <%s> block is not closed", tagAnalysis)
		}
		base = analysis + end + len("</"+tagAnalysis+">")
		rest = text[base:]
	}
	open, closing := "<"+tag+">", "</"+tag+">"
	i := strings.Index(rest, open)
	if i < 0 {
		if analysis < 0 {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("no <%s> block follows the <%s> block", tag, tagAnalysis)
	}
	start := i + len(open)
	j := strings.Index(rest[start:], closing)
	if j < 0 {
		return "", 0, false, fmt.Errorf("the <%s> block is not closed", tag)
	}
	if strings.Contains(rest[start+j+len(closing):], open) {
		return "", 0, false, fmt.Errorf("the answer has more than one <%s> block", tag)
	}
	return rest[start : start+j], base + start, true, nil
}

// parseValue returns the JSON value in text with exactly the leniency of contract §6 step
// 3: surrounding whitespace and prose, and code fences, are stripped; the JSON itself is
// never repaired. The value must be an object, or an array when allowArray is set; with
// several values the first complete one is taken. The returned slice is the answer's own
// bytes. Errors say what was wrong and where (offsets from base, text's offset in the
// answer), without content.
func parseValue(text string, base int, allowArray bool) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("the answer is empty")
	}
	if json.Valid([]byte(trimmed)) && allowedTop(trimmed[0], allowArray) {
		return json.RawMessage(trimmed), nil
	}

	body := text
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
		body, base = text[start:end], base+start
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
