// Package pdftest writes small, valid PDFs for tests: A4 pages with Helvetica text, an
// optional embedded image and no text, or an encryption dictionary nobody can open.
package pdftest

import (
	"bytes"
	"fmt"
	"strings"
)

// Page is one page of a generated PDF. Lines are drawn top-down in Helvetica 11 pt; an
// Image page draws a gray 8×8 image over the page and has no text layer.
type Page struct {
	Lines []string
	Image bool
}

// TextPage is a page with one line per argument.
func TextPage(lines ...string) Page { return Page{Lines: lines} }

// Pages returns n text pages whose first line is "Page i", followed by lines of filler
// of minChars characters in all (wrapped at 80).
func Pages(n, minChars int) []Page {
	out := make([]Page, n)
	filler := strings.Repeat("lorem ipsum dolor sit amet ", minChars/27+1)[:minChars]
	for i := range out {
		lines := []string{fmt.Sprintf("Page %d", i+1)}
		for rest := filler; rest != ""; {
			n := min(80, len(rest))
			lines = append(lines, rest[:n])
			rest = rest[n:]
		}
		out[i] = TextPage(lines...)
	}
	return out
}

// Build writes a PDF 1.4 file with the given pages and a correct cross-reference table.
func Build(pages ...Page) []byte { return build(pages, false) }

// Encrypted is Build with a Standard security handler whose user password is unknown, so
// a reader without the password refuses it.
func Encrypted(pages ...Page) []byte { return build(pages, true) }

func build(pages []Page, encrypt bool) []byte {
	var objs []string // objs[i] is object i+1
	add := func(body string) int {
		objs = append(objs, body)
		return len(objs)
	}
	catalog := add("") // filled once the page tree is known
	tree := add("")
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	img := add(grayImage())
	kids := make([]string, 0, len(pages))
	for _, p := range pages {
		content := pageContent(p)
		stream := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		page := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] "+
			"/Resources << /Font << /F1 %d 0 R >> /XObject << /Im1 %d 0 R >> >> /Contents %d 0 R >>",
			tree, font, img, stream))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", tree)
	objs[tree-1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	encryptRef := ""
	if encrypt {
		pad := strings.Repeat("ab", 32) // 32 bytes that match no password
		enc := add(fmt.Sprintf("<< /Filter /Standard /V 1 /R 2 /O <%s> /U <%s> /P -44 >>", pad, pad))
		encryptRef = fmt.Sprintf(" /Encrypt %d 0 R /ID [<0123456789abcdef0123456789abcdef> <0123456789abcdef0123456789abcdef>]", enc)
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root %d 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalog, encryptRef, xref)
	return b.Bytes()
}

func pageContent(p Page) string {
	var b strings.Builder
	if p.Image {
		b.WriteString("q 500 0 0 700 47 70 cm /Im1 Do Q\n")
	}
	if len(p.Lines) == 0 {
		return b.String()
	}
	b.WriteString("BT /F1 11 Tf 14 TL 50 790 Td\n")
	for _, line := range p.Lines {
		fmt.Fprintf(&b, "(%s) Tj T*\n", escape(line))
	}
	b.WriteString("ET")
	return b.String()
}

// grayImage is an 8×8 DeviceGray image with a diagonal gradient, uncompressed.
func grayImage() string {
	var px []byte
	for y := range 8 {
		for x := range 8 {
			px = append(px, byte((x+y)*16))
		}
	}
	return fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceGray "+
		"/BitsPerComponent 8 /Length %d >>\nstream\n%s\nendstream", len(px), px)
}

func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}
