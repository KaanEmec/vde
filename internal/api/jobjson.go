package api

import (
	"encoding/json"
	"time"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/job"
)

// jobResponse is the job resource of contract §5, with the superset fields
// judge_confidence and attachments (api/openapi.yaml, Job). result and error are null
// unless set; feedback is an array, never null; finished_at is null until the terminal
// state. Timestamps are RFC 3339 UTC with second precision.
type jobResponse struct {
	ID              string               `json:"id"`
	Status          job.Status           `json:"status"`
	Result          json.RawMessage      `json:"result"`
	Feedback        json.RawMessage      `json:"feedback"`
	Attempts        int                  `json:"attempts"`
	ServiceVersion  string               `json:"service_version"`
	JudgeConfidence *float64             `json:"judge_confidence"`
	Error           json.RawMessage      `json:"error"`
	Attachments     []attachmentResponse `json:"attachments,omitempty"`
	CreatedAt       string               `json:"created_at"`
	FinishedAt      *string              `json:"finished_at"`
	ExpiresAt       string               `json:"expires_at"`
}

// attachmentResponse is one attachment of an email document (metadata only).
type attachmentResponse struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
}

// emptyFeedback is the feedback of a job without any.
var emptyFeedback = json.RawMessage("[]")

// jobToResponse shapes a job row as the contract's job resource. Attachments are not on
// the row: the submit handler sets them from the parsed document; other callers leave
// them out (withAttachments).
func jobToResponse(j *job.Job) jobResponse {
	resp := jobResponse{
		ID:              j.ID,
		Status:          j.Status,
		Result:          nullIfEmpty(j.Result),
		Feedback:        j.Feedback,
		Attempts:        j.Attempts,
		ServiceVersion:  j.ServiceVersion,
		JudgeConfidence: j.JudgeConfidence,
		Error:           nullIfEmpty(j.Error),
		CreatedAt:       formatTime(j.CreatedAt),
		ExpiresAt:       formatTime(j.ExpiresAt),
	}
	if len(resp.Feedback) == 0 || string(resp.Feedback) == "null" {
		resp.Feedback = emptyFeedback
	}
	if !j.FinishedAt.IsZero() {
		f := formatTime(j.FinishedAt)
		resp.FinishedAt = &f
	}
	return resp
}

// withAttachments returns resp listing the attachments of an email document; other
// documents have none and the field stays absent.
func (resp jobResponse) withAttachments(atts []document.Attachment) jobResponse {
	if len(atts) == 0 {
		return resp
	}
	resp.Attachments = make([]attachmentResponse, len(atts))
	for i, a := range atts {
		resp.Attachments[i] = attachmentResponse{Name: a.Filename, MediaType: a.MediaType, Bytes: a.Size}
	}
	return resp
}

// nullIfEmpty keeps a JSON column as it is, nil (null) when unset.
func nullIfEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// formatTime is the contract's timestamp form: RFC 3339, UTC, whole seconds.
func formatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}
