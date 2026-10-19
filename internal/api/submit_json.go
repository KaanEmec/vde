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
	msgDocumentData      = "The document needs data_base64."
	msgBadBase64         = "The %s data_base64 is not valid standard base64."
	msgSchemaNotObject   = "The schema must be a JSON object."
	jsonInstructionsType = "text/plain"
	jsonSchemaType       = "application/schema+json"
)

// jsonSubmission is the application/json body of POST /v1/jobs and POST /v1/extract:
// the multipart parts as fields, the document and pages base64-encoded.
type jsonSubmission struct {
	Document *struct {
		MediaType  string  `json:"media_type"`
		DataBase64 *string `json:"data_base64"`
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
// shared. It returns the refusal of the first violation.
func readJSONSubmission(r *http.Request, cfg *config.Config, requestLimit int64) (*submission, error) {
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
	sub := &submission{}
	if err := in.documentParts(sub, cfg.Limits); err != nil {
		return nil, err
	}
	if err := in.textParts(sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// documentParts sets the decoded document and pages of sub from in, under the limits.
func (in *jsonSubmission) documentParts(sub *submission, limits config.Limits) error {
	switch d := in.Document; {
	case d == nil:
		return newError(codeBadRequest, msgPartMissing, partDocument)
	case d.MediaType == "":
		return newError(codeBadRequest, msgPartNoMediaType, partDocument)
	case d.DataBase64 == nil:
		return newError(codeBadRequest, msgDocumentData)
	}
	data, err := decodeBase64(partDocument, *in.Document.DataBase64, limits.DocumentBytes)
	if err != nil {
		return err
	}
	sub.document = newPart(in.Document.MediaType, in.Document.Filename, data)
	if len(in.Pages) > limits.Pages {
		return newError(codePayloadTooLarge, msgTooManyPages, limits.Pages)
	}
	for _, p := range in.Pages {
		data, err := decodeBase64(partPage, p.DataBase64, limits.PageBytes)
		if err != nil {
			return err
		}
		sub.pages = append(sub.pages, *newPart(p.MediaType, "", data))
	}
	return nil
}

// textParts sets the instructions, the schema and the options of sub from in, under
// their limits.
func (in *jsonSubmission) textParts(sub *submission) error {
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
			return newError(codeBadRequest, msgPartMissing, f.name)
		case len(*f.v) > maxInstructionsBytes:
			return newError(codePayloadTooLarge, msgPartTooLarge, f.name)
		}
		*f.slot = newPart(jsonInstructionsType, "", []byte(*f.v))
	}
	if schema := bytes.TrimSpace(in.Schema); len(schema) > 0 && string(schema) != "null" {
		switch {
		case len(schema) > maxSchemaBytes:
			return newError(codePayloadTooLarge, msgPartTooLarge, partSchema)
		case schema[0] != '{':
			return newError(codeInvalidSchema, msgSchemaNotObject)
		}
		sub.schema = newPart(jsonSchemaType, "", schema)
	}
	if in.Retention != nil {
		sub.retention = newPart("text/plain", "", []byte(*in.Retention))
	}
	if in.Judge != nil {
		if len(*in.Judge) > maxJudgeBytes {
			return optionError(partJudge)
		}
		sub.judge = newPart("text/plain", "", []byte(*in.Judge))
	}
	// The integer's literal goes through the multipart rules: "3" (a string), 3.0 or -1
	// are refused there.
	if v := bytes.TrimSpace(in.MaxAttempts); len(v) > 0 && string(v) != "null" {
		sub.maxAttempts = newPart("text/plain", "", v)
	}
	return nil
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
