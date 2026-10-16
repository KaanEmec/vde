package document

import "fmt"

// Code is the machine-readable reason a document is refused. The API layer maps it to an
// HTTP status: bad_request 400, payload_too_large 413, unsupported_media_type and
// unsupported_document 415.
type Code string

// Error codes.
const (
	CodeBadRequest           Code = "bad_request"
	CodePayloadTooLarge      Code = "payload_too_large"
	CodeUnsupportedMediaType Code = "unsupported_media_type"
	CodeUnsupportedDocument  Code = "unsupported_document"
)

// Error is a refused document. Msg never contains document content: only sizes, counts
// and media types.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return "document: " + string(e.Code)
	}
	return "document: " + string(e.Code) + ": " + e.Msg
}

// Is makes errors.Is(err, ErrPayloadTooLarge) match any *Error with that code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is; errors.As with *Error gives the message too.
var (
	ErrBadRequest           = &Error{Code: CodeBadRequest}
	ErrPayloadTooLarge      = &Error{Code: CodePayloadTooLarge}
	ErrUnsupportedMediaType = &Error{Code: CodeUnsupportedMediaType}
	ErrUnsupportedDocument  = &Error{Code: CodeUnsupportedDocument}
)

// refuse builds an *Error. Arguments must never be document content.
func refuse(code Code, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
