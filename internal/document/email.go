package document

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
)

// Bounds on an email's MIME tree, beyond the document size limit.
const (
	maxEmailDepth = 16   // nested multiparts
	maxEmailParts = 1000 // leaf and multipart parts together
	maxFilename   = 255  // bytes
)

// emailHeaders are the headers that carry meaning for extraction, in output order.
var emailHeaders = []string{"From", "To", "Cc", "Date", "Subject", "Message-ID", "In-Reply-To"}

// Attachment is an email part that is not the body. It is listed, never read, so the
// caller can submit the ones it wants as their own jobs. Inline images referenced by
// cid: and nested messages (message/rfc822) are attachments too.
type Attachment struct {
	Filename  string // RFC 2047/2231 decoded, without directory components; may be empty
	MediaType string // lowercase, without parameters
	Size      int64  // bytes after transfer decoding
}

// email is a parsed message/rfc822 document.
type email struct {
	header      string // one "Name: value" line per present header of emailHeaders
	body        []byte // UTF-8
	sent        string // the Date header as RFC 3339 with its offset, "" when absent or unparseable
	html        bool
	attachments []Attachment
}

// text is the header block, a blank line, then the body (HTML under policy).
func (e *email) text(policy HTMLPolicy) string {
	body := string(e.body)
	if e.html {
		body = htmlText(e.body, policy)
	}
	return e.join(body)
}

// labelledText is text with a label line before and after each forwarded message and
// quoted earlier reply of the body (sections.go). An HTML body is labelled under
// visible_text only: raw_html is the source as it is.
func (e *email) labelledText(policy HTMLPolicy) string {
	switch {
	case !e.html:
		return e.join(labelPlain(string(e.body)))
	case policy == VisibleText:
		return e.join(visibleTextLabelled(e.body, maxVisibleText))
	}
	return e.text(policy)
}

func (e *email) join(body string) string {
	if e.header == "" {
		return body
	}
	return e.header + "\n" + body
}

// parseEmail reads a message: the first text/html body (else the first text/plain) that
// is not an attachment becomes the body, every other leaf part is an attachment.
func parseEmail(data []byte) (*email, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil || len(msg.Header) == 0 {
		return nil, refuse(CodeBadRequest, "email headers are unreadable")
	}
	var w emailWalker
	if err := w.walk(textproto.MIMEHeader(msg.Header), msg.Body, 0); err != nil {
		return nil, err
	}
	e := &email{header: headerBlock(msg.Header), attachments: w.attachments}
	e.sent = sentTime(msg.Header.Date())
	switch {
	case w.html != nil:
		e.body, e.html = w.html, true
	case w.plain != nil:
		e.body = w.plain
	default:
		return nil, refuse(CodeUnsupportedDocument, "email has no text or HTML body")
	}
	return e, nil
}

type emailWalker struct {
	parts       int
	html, plain []byte
	attachments []Attachment
}

func (w *emailWalker) walk(h textproto.MIMEHeader, r io.Reader, depth int) error {
	if w.parts++; w.parts > maxEmailParts {
		return refuse(CodeBadRequest, "email has more than %d parts", maxEmailParts)
	}
	mediaType, params := "text/plain", map[string]string{}
	if ct := h.Get("Content-Type"); ct != "" {
		var err error
		if mediaType, params, err = mime.ParseMediaType(ct); err != nil {
			mediaType = "application/octet-stream"
		}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		return w.multipart(r, params["boundary"], depth)
	}

	disposition, dparams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	filename := dparams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	body := transferDecoder(h.Get("Content-Transfer-Encoding"), r)
	isHTML := mediaType == "text/html"
	if (isHTML || mediaType == "text/plain") && disposition != "attachment" && filename == "" {
		target := &w.plain
		if isHTML {
			target = &w.html
		}
		if *target != nil {
			return nil // a later alternative of a kind already chosen
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			return refuse(CodeBadRequest, "email body cannot be decoded")
		}
		text, err := toUTF8(raw, params["charset"], isHTML, false)
		if err != nil {
			return err
		}
		*target = text
		return nil
	}
	size, _ := io.Copy(io.Discard, body) // a corrupt encoding lists the bytes decoded so far
	w.attachments = append(w.attachments, Attachment{
		Filename:  cleanFilename(filename),
		MediaType: mediaType,
		Size:      size,
	})
	return nil
}

func (w *emailWalker) multipart(r io.Reader, boundary string, depth int) error {
	if depth >= maxEmailDepth {
		return refuse(CodeBadRequest, "email nests more than %d multiparts", maxEmailDepth)
	}
	if boundary == "" {
		return refuse(CodeBadRequest, "email multipart has no boundary")
	}
	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextRawPart() // raw: transfer encodings are decoded here, uniformly
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return refuse(CodeBadRequest, "email multipart is malformed")
		}
		if err := w.walk(part.Header, part, depth+1); err != nil {
			return err
		}
	}
}

func transferDecoder(encoding string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	}
	return r // 7bit, 8bit, binary
}

// headerBlock renders the meaningful headers, RFC 2047 decoded, one per line.
func headerBlock(h mail.Header) string {
	var b strings.Builder
	for _, name := range emailHeaders {
		values := h[textproto.CanonicalMIMEHeaderKey(name)]
		decoded := make([]string, 0, len(values))
		for _, v := range values {
			if v = decodeHeader(v); v != "" {
				decoded = append(decoded, v)
			}
		}
		if len(decoded) > 0 {
			b.WriteString(name + ": " + strings.Join(decoded, ", ") + "\n")
		}
	}
	return b.String()
}

var wordDecoder = &mime.WordDecoder{
	CharsetReader: func(label string, r io.Reader) (io.Reader, error) {
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, err
		}
		return enc.NewDecoder().Reader(r), nil
	},
}

// decodeHeader decodes RFC 2047 words and returns the value on one line: control
// characters (a decoded word may hold a newline) become spaces, whitespace collapses.
func decodeHeader(v string) string {
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		v = d
	}
	v = strings.ToValidUTF8(v, "\ufffd")
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	return strings.Join(strings.Fields(v), " ")
}

// cleanFilename decodes a filename parameter and keeps only its last path element, so
// a caller that saves the attachment under this name cannot be steered elsewhere.
func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(decodeHeader(name), `\`, "/"))
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	if len(name) > maxFilename {
		cut := maxFilename
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut]
	}
	return name
}
