package document

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
)

const corpusDir = "../../testdata/travelinbox/corpus"

func corpusEmail(t *testing.T, name string) *Document {
	t.Helper()
	return newEmail(t, filepath.Join(corpusDir, name, "source.eml"))
}

func newEmail(t *testing.T, path string) *Document {
	t.Helper()
	doc, err := New(Input{MediaType: "message/rfc822", Data: readFile(t, path)}, testLimits)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc
}

func TestEmailFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file        string
		header      string
		body        string // expected text after the header block, under raw_html
		visible     string // expected body under visible_text
		attachments []Attachment
	}{
		{
			file: "mixed-pdf.eml",
			header: "From: José García <jose@example.com>\n" +
				"To: Ana <ana@example.com>, bob@example.com\n" +
				"Cc: travel@example.com\n" +
				"Date: Mon, 01 Mar 2027 09:00:00 +0100\n" +
				"Subject: Ihre Reise nach Zürich\n" +
				"Message-ID: <abc@example.com>\n" +
				"In-Reply-To: <prev@example.com>\n",
			body:    `<html><head><title>Booking</title><style></style></head><body><p>Booking <b>ABC123</b> confirmed.</p><img src="cid:logo" alt="Logo"></body></html>`,
			visible: "Booking\n\nBooking ABC123 confirmed.\n\nLogo",
			attachments: []Attachment{
				{Filename: "", MediaType: "image/png", Size: 8},
				{Filename: "Fahrkarte Zürich.pdf", MediaType: "application/pdf", Size: 9},
				{Filename: "forwarded.eml", MediaType: "message/rfc822", Size: int64(len("From: a@example.com\nSubject: inner\n\ninner body"))},
			},
		},
		{
			file: "alternative.eml",
			header: "From: Shop <shop@example.com>\nTo: you@example.com\n" +
				"Date: Wed, 03 Mar 2027 11:00:00 +0000\nSubject: Your order\n",
			body:    `<html><body><table><tr><td>Total</td><td>120 €</td></tr></table><p>Thanks, <a href="https://example.com/b/1">manage booking</a></p></body></html>`,
			visible: "Total\t120 €\n\nThanks, manage booking (https://example.com/b/1)",
		},
		{
			file: "latin1.eml",
			header: "From: André <andre@example.com>\nTo: x@example.com\n" +
				"Date: Tue, 02 Mar 2027 10:00:00 +0100\nSubject: Réservation confirmée\n",
			body:    "Votre réservation à Genève est confirmée.\n",
			visible: "Votre réservation à Genève est confirmée.\n",
		},
		{
			file: "text-only.eml",
			header: "From: friend@example.com\nTo: me@example.com\n" +
				"Date: Thu, 04 Mar 2027 12:00:00 +0000\nSubject: Dinner\n",
			body:    "Table for two at 19:30, reference R-778.\n",
			visible: "Table for two at 19:30, reference R-778.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()
			doc := newEmail(t, filepath.Join("testdata", "eml", tt.file))
			for policy, body := range map[HTMLPolicy]string{RawHTML: tt.body, VisibleText: tt.visible} {
				got, err := doc.Text(policy)
				if err != nil {
					t.Fatal(err)
				}
				if want := tt.header + "\n" + body; got != want {
					t.Errorf("%s text:\n%q\nwant\n%q", policy, got, want)
				}
			}
			if !reflect.DeepEqual(doc.Attachments, tt.attachments) {
				t.Errorf("attachments = %+v, want %+v", doc.Attachments, tt.attachments)
			}
		})
	}
}

func TestEmailRefused(t *testing.T) {
	t.Parallel()
	nest := func(depth int) string {
		var b strings.Builder
		b.WriteString("From: a@example.com\nContent-Type: multipart/mixed; boundary=\"b0\"\n\n")
		for i := 1; i <= depth; i++ {
			b.WriteString("--b" + strconv.Itoa(i-1) + "\nContent-Type: multipart/mixed; boundary=\"b" + strconv.Itoa(i) + "\"\n\n")
		}
		return b.String()
	}
	parts := func(n int) string {
		var b strings.Builder
		b.WriteString("From: a@example.com\nContent-Type: multipart/mixed; boundary=\"b\"\n\n")
		for range n {
			b.WriteString("--b\nContent-Type: image/png\n\nx\n")
		}
		b.WriteString("--b--\n")
		return b.String()
	}
	tests := []struct {
		name string
		data string
		want Code
	}{
		{"no body", string(readFile(t, "testdata/eml/no-body.eml")), CodeUnsupportedDocument},
		{"too deep", nest(maxEmailDepth + 1), CodeBadRequest},
		{"too many parts", parts(maxEmailParts), CodeBadRequest},
		{"no boundary", "From: a@example.com\nContent-Type: multipart/mixed\n\nx", CodeBadRequest},
		{"no headers", "\n\nbody", CodeBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Input{MediaType: "message/rfc822", Data: []byte(tt.data)}, testLimits)
			wantCode(t, err, tt.want)
		})
	}
	doc, err := New(Input{MediaType: "message/rfc822", Data: []byte("From: a@example.com\n\nbody")}, config.Limits{DocumentBytes: 5, PageBytes: 10 << 20, Pages: 20})
	if doc != nil {
		t.Fatal("over-limit email accepted")
	}
	wantCode(t, err, CodePayloadTooLarge)
}

func TestCleanFilename(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"ticket.pdf":                       "ticket.pdf",
		"../../etc/passwd":                 "passwd",
		`C:\Users\a\invoice.pdf`:           "invoice.pdf",
		"=?UTF-8?Q?R=C3=A9servation.pdf?=": "Réservation.pdf",
		"..":                               "",
		"":                                 "",
		"a\nb.pdf":                         "a b.pdf",
		strings.Repeat("é", 200):           strings.Repeat("é", 127),
	}
	for in, want := range tests {
		if got := cleanFilename(in); got != want {
			t.Errorf("cleanFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailCorpus(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	withPDF := map[string]string{
		"001-flight": "Reiseplan_VT6Y7Y.pdf",
		"005-flight": "Recu_billet_electronique_7VT9MR.pdf",
		"023-hotel":  "Confirmation_2XR5T9CJ.pdf",
		"041-rail":   "Ticket_150163649771.pdf",
		"045-rail":   "Amtrak_eTicket_0N3O1D.pdf",
	}
	seen := 0
	for _, e := range entries {
		path := filepath.Join(corpusDir, e.Name(), "source.eml")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		seen++
		doc := newEmail(t, path)
		text, err := doc.Text(VisibleText)
		if err != nil || !strings.HasPrefix(text, "From: ") || !strings.Contains(text, "\nSubject: ") {
			t.Errorf("%s: text = %.80q, %v", e.Name(), text, err)
		}
		if strings.Contains(text, "Received:") || strings.Contains(text, "MIME-Version") {
			t.Errorf("%s: unwanted headers in text", e.Name())
		}
		if name, ok := withPDF[e.Name()]; ok {
			if len(doc.Attachments) != 1 || doc.Attachments[0].Filename != name ||
				doc.Attachments[0].MediaType != "application/pdf" || doc.Attachments[0].Size < 100 {
				t.Errorf("%s: attachments = %+v", e.Name(), doc.Attachments)
			}
		} else if len(doc.Attachments) != 0 {
			t.Errorf("%s: unexpected attachments %+v", e.Name(), doc.Attachments)
		}
	}
	if seen < 40 {
		t.Errorf("found %d corpus emails, want at least 40", seen)
	}
}

// TestEmailMatchesHTMLBody is the acceptance scenario of job 04: an .eml booking
// confirmation gives the models the same text as its HTML body submitted directly,
// preceded only by the header block.
func TestEmailMatchesHTMLBody(t *testing.T) {
	t.Parallel()
	raw := readFile(t, filepath.Join(corpusDir, "019-hotel", "source.eml"))
	htmlBody := extractHTMLPart(t, raw)
	caps := capsFor(t, []string{"application/pdf", "text/plain"})
	for _, policy := range []HTMLPolicy{RawHTML, VisibleText} {
		emailDoc, err := New(Input{MediaType: "message/rfc822", Data: raw}, testLimits)
		if err != nil {
			t.Fatal(err)
		}
		htmlDoc, err := New(Input{MediaType: "text/html; charset=UTF-8", Data: htmlBody}, testLimits)
		if err != nil {
			t.Fatal(err)
		}
		fromEmail, err := Select(t.Context(), emailDoc, caps, caps, policy, ImagePolicy{}, PDFPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		fromHTML, err := Select(t.Context(), htmlDoc, caps, caps, policy, ImagePolicy{}, PDFPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		_, mail, _ := strings.Cut(fromEmail.Parts[0].Text, "\n\n") // after the context line
		header, body, ok := strings.Cut(mail, "\n\n")
		_, htmlText, _ := strings.Cut(fromHTML.Parts[0].Text, "\n\n")
		if !ok || !strings.HasPrefix(header, "From: ") || body != htmlText {
			t.Errorf("%s: email body differs from the HTML document", policy)
		}
		if fromEmail.HTMLPolicy != policy || fromHTML.HTMLPolicy != policy {
			t.Errorf("%s: HTMLPolicy = %q, %q", policy, fromEmail.HTMLPolicy, fromHTML.HTMLPolicy)
		}
	}
}

// extractHTMLPart pulls the text/html part out of a multipart/alternative message with
// the standard library only, independently of parseEmail.
func extractHTMLPart(t *testing.T, raw []byte) []byte {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextRawPart()
		if err != nil {
			t.Fatal("no text/html part")
		}
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
			body, err := io.ReadAll(quotedprintable.NewReader(p))
			if err != nil {
				t.Fatal(err)
			}
			return body
		}
	}
}
