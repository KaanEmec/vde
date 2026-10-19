package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kaanemec/vde/internal/config"
)

// Messages of the JSON submission path.
const (
	msgMalformedJSON     = "The JSON body is malformed or has a field this service does not define."
	msgDocumentSource    = "The document needs exactly one of data_base64 and url."
	msgBadBase64         = "The %s data_base64 is not valid standard base64."
	msgSchemaNotObject   = "The schema must be a JSON object."
	msgFetchDisabled     = "Document URLs are not enabled on this service."
	msgFetchRefused      = "The document url is not an https URL on an allowed host."
	msgFetchFailed       = "The document url could not be fetched."
	msgFetchedTooLarge   = "The document at the url exceeds its size limit."
	jsonInstructionsType = "text/plain"
	jsonSchemaType       = "application/schema+json"
)

// jsonSubmission is the application/json body of POST /v1/jobs and POST /v1/extract:
// the multipart parts as fields, the document and pages base64-encoded (or the document
// by url, documents.fetch).
type jsonSubmission struct {
	Document *struct {
		MediaType  string  `json:"media_type"`
		DataBase64 *string `json:"data_base64"`
		URL        string  `json:"url"`
		Filename   string  `json:"filename"`
	} `json:"document"`
	Pages []struct {
		MediaType  string `json:"media_type"`
		DataBase64 string `json:"data_base64"`
	} `json:"pages"`
	ExtractorInstructions *string         `json:"extractor_instructions"`
	JudgeInstructions     *string         `json:"judge_instructions"`
	Schema                json.RawMessage `json:"schema"`
	Retention             *string         `json:"retention"`
	MaxAttempts           json.RawMessage `json:"max_attempts"` // a JSON integer
	Judge                 *string         `json:"judge"`        // always, never or auto (allow_override only)
}

// readJSONSubmission decodes a JSON body into the same submission the multipart path
// builds, with the same limits applied to the decoded bytes, so the checks after it are
// shared. It reports false with the refusal on the first violation.
func (s *server) readJSONSubmission(r *http.Request, cfg *config.Config, requestLimit int64) (*submission, error) {
	var in jsonSubmission
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&in)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("api: data after the JSON body")
	}
	if err != nil {
		if e := readError(r, err, requestLimit); e.code == codePayloadTooLarge {
			return nil, e
		}
		return nil, newError(codeBadRequest, msgMalformedJSON)
	}
	limits := cfg.Limits
	docLimit, pageLimit, maxPages := limits.DocumentBytes, limits.PageBytes, limits.Pages
	sub := &submission{}
	switch d := in.Document; {
	case d == nil:
		return nil, newError(codeBadRequest, msgPartMissing, partDocument)
	case d.MediaType == "":
		return nil, newError(codeBadRequest, msgPartNoMediaType, partDocument)
	case (d.DataBase64 == nil) == (d.URL == ""):
		return nil, newError(codeBadRequest, msgDocumentSource)
	case d.DataBase64 != nil:
		data, err := decodeBase64(partDocument, *d.DataBase64, docLimit)
		if err != nil {
			return nil, err
		}
		sub.document = newPart(d.MediaType, d.Filename, data)
	case s.fetch == nil:
		return nil, newError(codeBadRequest, msgFetchDisabled)
	default: // fetched after the submission limits (fetchPending)
		sub.document = &part{mediaType: d.MediaType, filename: d.Filename}
		sub.documentURL = d.URL
	}
	if len(in.Pages) > maxPages {
		return nil, newError(codePayloadTooLarge, msgTooManyPages, maxPages)
	}
	for _, p := range in.Pages {
		data, err := decodeBase64(partPage, p.DataBase64, pageLimit)
		if err != nil {
			return nil, err
		}
		sub.pages = append(sub.pages, *newPart(p.MediaType, "", data))
	}
	for _, f := range []struct {
		name string
		v    *string
		slot **part
	}{
		{partExtractorInstructions, in.ExtractorInstructions, &sub.extractorInstructions},
		{partJudgeInstructions, in.JudgeInstructions, &sub.judgeInstructions},
	} {
		switch {
		case f.v == nil:
			return nil, newError(codeBadRequest, msgPartMissing, f.name)
		case len(*f.v) > maxInstructionsBytes:
			return nil, newError(codePayloadTooLarge, msgPartTooLarge, f.name)
		}
		*f.slot = newPart(jsonInstructionsType, "", []byte(*f.v))
	}
	if schema := bytes.TrimSpace(in.Schema); len(schema) > 0 && string(schema) != "null" {
		switch {
		case len(schema) > maxSchemaBytes:
			return nil, newError(codePayloadTooLarge, msgPartTooLarge, partSchema)
		case schema[0] != '{':
			return nil, newError(codeInvalidSchema, msgSchemaNotObject)
		}
		sub.schema = newPart(jsonSchemaType, "", schema)
	}
	if in.Retention != nil {
		sub.retention = newPart("text/plain", "", []byte(*in.Retention))
	}
	if in.Judge != nil {
		if len(*in.Judge) > maxJudgeBytes {
			return nil, optionError(partJudge)
		}
		sub.judge = newPart("text/plain", "", []byte(*in.Judge))
	}
	// The integer's literal goes through the multipart rules: "3" (a string), 3.0 or -1
	// are refused there.
	if v := bytes.TrimSpace(in.MaxAttempts); len(v) > 0 && string(v) != "null" {
		sub.maxAttempts = newPart("text/plain", "", v)
	}
	return sub, nil
}

// decodeBase64 decodes a field of the named part under the part's limit.
func decodeBase64(name, s string, limit int64) ([]byte, error) {
	if int64(base64.StdEncoding.DecodedLen(len(s))) > limit+2 { // padding slack
		return nil, newError(codePayloadTooLarge, msgPartTooLarge, name)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	switch {
	case err != nil:
		return nil, newError(codeBadRequest, msgBadBase64, name)
	case int64(len(data)) > limit:
		return nil, newError(codePayloadTooLarge, msgPartTooLarge, name)
	}
	return data, nil
}

func newPart(mediaType, filename string, data []byte) *part {
	return &part{mediaType: mediaType, filename: filename, data: data, sum: sha256.Sum256(data)}
}

// fetchPending fetches the document url of a JSON submission, if it has one, into
// sub.document under documents.fetch (fetch.go). createJob calls it after the submission
// limits, so a refused or rate-limited caller makes the service fetch nothing; decoding
// already refused a url when fetching is disabled.
func (s *server) fetchPending(r *http.Request, sub *submission, limits config.Limits) error {
	if sub.documentURL == "" {
		return nil
	}
	data, err := s.fetch.get(r.Context(), sub.documentURL, limits.DocumentBytes)
	switch {
	case errors.Is(err, errFetchRefused):
		return newError(codeBadRequest, msgFetchRefused)
	case errors.Is(err, errFetchTooLarge):
		return newError(codePayloadTooLarge, msgFetchedTooLarge)
	case err != nil:
		return newError(codeBadRequest, msgFetchFailed)
	}
	sub.document = newPart(sub.document.mediaType, sub.document.filename, data)
	sub.documentURL = ""
	return nil
}
