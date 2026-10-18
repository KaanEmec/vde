package api

import (
	"encoding/json"
	"time"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
)

// jobResponse is the job resource of contract §5, with the superset fields
// judge_confidence, verification, attachments and content_deleted_at (api/openapi.yaml,
// Job). verification is null until the terminal state, then always present. result
// and error are null unless set; feedback is an array, never null; finished_at is null
// until the terminal state; content_deleted_at is present only on a job whose content is
// gone, which only an idempotent replay shows. Timestamps are RFC 3339 UTC with second
// precision.
type jobResponse struct {
	ID               string           `json:"id"`
	Status           job.Status       `json:"status"`
	Result           json.RawMessage  `json:"result"`
	Feedback         json.RawMessage  `json:"feedback"`
	Attempts         int              `json:"attempts"`
	ServiceVersion   string           `json:"service_version"`
	JudgeConfidence  *float64         `json:"judge_confidence"`
	Verification     *string          `json:"verification"`
	Error            json.RawMessage  `json:"error"`
	Attachments      []job.Attachment `json:"attachments,omitempty"`
	CreatedAt        string           `json:"created_at"`
	FinishedAt       *string          `json:"finished_at"`
	ExpiresAt        string           `json:"expires_at"`
	ContentDeletedAt *string          `json:"content_deleted_at,omitempty"`
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
		Feedback:        extract.VisibleFeedback(j.JudgePolicy, j.Feedback), // a running job's saved feedback may hold a repair's entries
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
	if j.Status.Terminal() {
		v := j.Verification
		if v == "" {
			v = job.DefaultVerification(j.Status)
		}
		resp.Verification = &v
	}
	if !j.FinishedAt.IsZero() {
		f := formatTime(j.FinishedAt)
		resp.FinishedAt = &f
	}
	if !j.ContentDeletedAt.IsZero() {
		d := formatTime(j.ContentDeletedAt)
		resp.ContentDeletedAt = &d
	}
	return resp
}

// replayResponse is the job resource an idempotent replay answers. A job whose content
// is gone (read once under retention: none, or past its expires_at, swept or not yet)
// keeps its id and status but answers result null, feedback [], error null and
// content_deleted_at set: the result was delivered or has expired, and the client must
// not re-submit (docs/api/guide.md). A terminal retention: none job not read yet answers
// the same without content_deleted_at: its content is kept for the first GET, which
// delivers and deletes it (contract §10), and a replay never does either.
func replayResponse(j job.Job, now time.Time) jobResponse {
	live := j.ContentDeletedAt.IsZero() && now.Before(j.ExpiresAt)
	if live && (j.Retention != job.RetentionNone || !j.Status.Terminal()) {
		return jobToResponse(&j)
	}
	if !live && j.ContentDeletedAt.IsZero() {
		j.ContentDeletedAt = j.ExpiresAt
	}
	j.Result, j.Feedback, j.Error, j.JudgeConfidence, j.Attachments = nil, nil, nil, nil, nil
	return jobToResponse(&j)
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
