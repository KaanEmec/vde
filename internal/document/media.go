package document

import (
	"bytes"
	"mime"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding/htmlindex"
)

// Category groups media types by how they are read.
type Category int

// Categories. The zero value is not a category.
const (
	CategoryText Category = iota + 1 // given to the models as text
	CategoryImage
	CategoryPDF
	CategoryEmail // message/rfc822, parsed into text
)

func (c Category) String() string {
	switch c {
	case CategoryText:
		return "text"
	case CategoryImage:
		return "image"
	case CategoryPDF:
		return "pdf"
	case CategoryEmail:
		return "email"
	}
	return "unknown"
}

// MediaType is one accepted document media type.
type MediaType struct {
	Name     string // lowercase, without parameters
	Category Category
	Required bool // a MUST type of contract §4; the others are SHOULD or a superset
}

// mediaTypes are the accepted document types: contract §4 MUST and SHOULD, plus
// message/rfc822 (decision 7).
var mediaTypes = []MediaType{
	{"application/pdf", CategoryPDF, true},
	{"image/jpeg", CategoryImage, true},
	{"image/png", CategoryImage, true},
	{"text/html", CategoryText, true},
	{"text/plain", CategoryText, true},
	{"image/webp", CategoryImage, false},
	{"image/heic", CategoryImage, false},
	{"text/markdown", CategoryText, false},
	{"message/rfc822", CategoryEmail, false},
}

// aliases are common non-canonical spellings.
var aliases = map[string]string{"image/jpg": "image/jpeg"}

// MediaTypes returns the accepted document media types.
func MediaTypes() []MediaType { return slices.Clone(mediaTypes) }

// LookupMediaType parses a Content-Type value and returns the accepted type it names
// and its parameters. It fails with ErrUnsupportedMediaType.
func LookupMediaType(contentType string) (MediaType, map[string]string, error) {
	name, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return MediaType{}, nil, refuse(CodeUnsupportedMediaType, "unreadable media type")
	}
	if a, ok := aliases[name]; ok {
		name = a
	}
	for _, mt := range mediaTypes {
		if mt.Name == name {
			return mt, params, nil
		}
	}
	return MediaType{}, nil, refuse(CodeUnsupportedMediaType, "media type %q is not accepted", name)
}

// sniff reports whether data starts like the binary media type name.
func sniff(name string, data []byte) bool {
	if name == "image/heic" {
		return isHEIC(data)
	}
	return http.DetectContentType(data) == name
}

// isHEIC checks for an ISO BMFF ftyp box with a HEIF brand; http.DetectContentType does
// not know HEIC.
func isHEIC(data []byte) bool {
	if len(data) < 12 || !bytes.Equal(data[4:8], []byte("ftyp")) {
		return false
	}
	switch string(data[8:12]) {
	case "heic", "heix", "heim", "heis", "hevc", "hevx", "mif1", "msf1":
		return true
	}
	return false
}

// toUTF8 decodes text in the named charset ("" when undeclared) to UTF-8. For HTML
// without a declared charset it follows the browser's order: byte order mark, then
// <meta charset>, then UTF-8 when the bytes are valid UTF-8, else windows-1252. Other
// text without a declared charset must be UTF-8. strict refuses invalid UTF-8; otherwise
// invalid sequences become U+FFFD.
func toUTF8(data []byte, label string, isHTML, strict bool) ([]byte, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" && isHTML {
		_, name, certain := charset.DetermineEncoding(data, "")
		switch {
		case certain || name != "windows-1252":
			label = name
		case !utf8.Valid(data):
			label = name // no declaration, not UTF-8: windows-1252 as browsers do
		}
	}
	out := data
	if label != "" && label != "us-ascii" && label != "ascii" {
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, refuse(CodeUnsupportedMediaType, "charset is not supported")
		}
		if name, _ := htmlindex.Name(enc); name != "utf-8" {
			if out, err = enc.NewDecoder().Bytes(data); err != nil {
				return nil, refuse(CodeBadRequest, "text cannot be decoded as %s", name)
			}
		}
	}
	if !utf8.Valid(out) {
		if strict {
			return nil, refuse(CodeBadRequest, "text is not valid UTF-8")
		}
		out = bytes.ToValidUTF8(out, []byte("\ufffd"))
	}
	return bytes.TrimPrefix(out, []byte("\ufeff")), nil
}
