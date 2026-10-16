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
	ID              string           `json:"id"`
	Status          job.Status       `json:"status"`
	Result          json.RawMessage  `json:"result"`
	Feedback        json.RawMessage  `json:"feedback"`
	Attempts        int              `json:"attempts"`
	ServiceVersion  string           `json:"service_version"`
	JudgeConfidence *float64         `json:"judge_confidence"`
	Error           json.RawMessage  `json:"error"`
	Attachments     []job.Attachment `json:"attachments,omitempty"`
	CreatedAt       string           `json:"created_at"`
	FinishedAt      *string          `json:"finished_at"`
	ExpiresAt       string           `json:"expires_at"`
}

// emptyFeedback is the feedback of a job without any.
var emptyFeedback = json.RawMessage("[]")

// jobToResponse shapes a job row as the contract's job resource, attachments included
// (stored on the row at submit, so polls and idempotent replays list them too).
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
		Attachments:     j.Attachments,
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

// jobAttachments is the stored form of an email document's attachments; nil for other
// documents, so the field stays absent.
func jobAttachments(atts []document.Attachment) []job.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]job.Attachment, len(atts))
	for i, a := range atts {
		out[i] = job.Attachment{Name: a.Filename, MediaType: a.MediaType, Bytes: a.Size}
	}
	return out
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
