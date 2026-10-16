package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"slices"
	"time"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
)

// Status is a job's terminal status (contract §5).
type Status string

// Terminal statuses. Only StatusAccepted is acceptance.
const (
	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"
	StatusFailed   Status = "failed"
)

// Job error codes (contract §9).
const (
	CodeProviderUnavailable = "provider_unavailable"
	CodeExtractorFailed     = "extractor_failed"
	CodeJudgeFailed         = "judge_failed"
	CodeTimeout             = "timeout"
	CodeInternal            = "internal"
)

// Feedback sources (contract §5).
const (
	SourceSchema = "schema"
	SourceJudge  = "judge"
)

// Loop defaults.
const (
	DefaultMaxAttempts          = 3     // contract §6
	DefaultFeedbackMessageChars = 4_000 // contract §7: at least 2,000
)

// Feedback is one issue with a candidate: an RFC 6901 pointer into it ("" for the whole
// candidate), a message for humans and its source, schema or judge.
type Feedback struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
	Source  string `json:"source"`
}

// JobError is the error of a failed job. Message is generic and never carries content.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Input is one job's extraction request.
type Input struct {
	// Representation is what both roles read; its parts are given to both unchanged.
	Representation        document.Representation
	ExtractorInstructions string  // verbatim, never edited
	JudgeInstructions     string  // verbatim, never edited
	Schema                *Schema // nil in schema-less mode
	// MaxAttempts bounds the extractor calls; 0 or less means DefaultMaxAttempts.
	MaxAttempts int
	// PriorAttempts, PriorCandidate and PriorFeedback resume a job whose earlier run
	// stopped (a crashed worker): the attempts already made, the last parseable candidate
	// (nil when none) and the feedback on it, or on an answer that was not JSON. The
	// first extractor request carries them as a retry would; attempts count on from
	// PriorAttempts, so at most MaxAttempts-PriorAttempts calls are made, and
	// Outcome.Attempts is the total.
	PriorAttempts  int
	PriorCandidate json.RawMessage
	PriorFeedback  []Feedback
	// Deadline is when the job must be terminal (created_at plus limits.job_timeout);
	// the zero time adds no deadline to ctx.
	Deadline time.Time
	// OnAttempt, when set, is called after each attempt that sends the loop on to
	// another attempt or to the attempt limit, so the caller can persist progress.
	OnAttempt func(Progress)
}

// Progress is the state after an attempt.
type Progress struct {
	Attempts int
	Result   json.RawMessage // the last parseable candidate, nil when none yet
	Feedback []Feedback      // the feedback on Result, or on an answer that was not JSON
}

// Outcome is a job's terminal state, shaped like the job resource of contract §5.
type Outcome struct {
	Status Status `json:"status"`
	// Result is the last parseable candidate exactly as the extractor produced it, or nil
	// (JSON null) when there is none. For StatusAccepted it is never nil and, when a
	// schema was given, valid.
	Result json.RawMessage `json:"result"`
	// Feedback is the final feedback on Result: the judge's on a rejection, the schema
	// validator's when Result does not validate, advisory judge entries on acceptance.
	Feedback []Feedback `json:"feedback"`
	Attempts int        `json:"attempts"` // extractor calls made, those of Input.PriorAttempts included
	// JudgeConfidence is the confidence of the last parsed verdict, nil when absent.
	JudgeConfidence *float64  `json:"judge_confidence"`
	Error           *JobError `json:"error"` // set exactly when Status is StatusFailed
}

// errTimeout and errUnavailable end the loop with their job error codes; any other
// error from a role call that is not a permanent provider failure is internal.
var (
	errTimeout     = errors.New("extract: job deadline exceeded")
	errUnavailable = errors.New("extract: endpoint group unavailable")
)

// Run runs the extraction loop of contract §6 for one job and returns its terminal
// state. It never returns StatusAccepted without a parsed accept verdict on a candidate
// that validates against in.Schema (when given). A panic inside the loop, including in
// provider code, ends the job as failed/internal.
func Run(ctx context.Context, in Input, roles Roles) (out Outcome) {
	if !in.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, in.Deadline)
		defer cancel()
	}
	l := &loop{in: &in, roles: roles, maxChars: roles.FeedbackMessageChars}
	if l.maxChars <= 0 {
		l.maxChars = DefaultFeedbackMessageChars
	}
	defer func() {
		if r := recover(); r != nil {
			attrs := []any{"stack", string(debug.Stack())}
			if re, ok := r.(runtime.Error); ok {
				attrs = append(attrs, "panic", re.Error())
			}
			slog.Error("extract: panic recovered", attrs...)
			out = l.failed(CodeInternal)
		}
	}()
	if roles.Extractor == nil || roles.Judge == nil {
		slog.Error("extract: roles without endpoint groups")
		return l.failed(CodeInternal)
	}
	return l.run(ctx)
}

// loop is the state of one Run.
type loop struct {
	in       *Input
	roles    Roles
	maxChars int

	attempts   int
	usable     bool            // some attempt produced parseable JSON
	result     json.RawMessage // the last parseable candidate
	feedback   []Feedback      // the feedback on result
	prev       json.RawMessage // the candidate for the next extractor request
	next       []Feedback      // the feedback for the next extractor request
	confidence *float64        // of the last parsed verdict
}

func (l *loop) run(ctx context.Context) Outcome {
	maxAttempts := l.in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	l.attempts = l.in.PriorAttempts
	if l.in.PriorCandidate != nil {
		l.usable, l.result, l.prev = true, l.in.PriorCandidate, l.in.PriorCandidate
	}
	l.feedback, l.next = l.in.PriorFeedback, l.in.PriorFeedback
	schemaless := l.in.Schema == nil
	extractorSystem, judgeSystem := framings(schemaless)
	allowArray := schemaless || l.in.Schema.allowArray

	for l.attempts < maxAttempts {
		if ctx.Err() != nil {
			return l.failed(CodeTimeout)
		}
		l.attempts++
		extractor := at(l.roles.Extractor, l.roles.ExtractorEscalation, l.attempts)
		resp, _, err := l.call(ctx, extractor, extractorSystem, assembleExtractor(l.in, l.prev, l.next))
		switch {
		case err == nil:
		case errors.Is(err, router.ErrPermanent):
			// A refusal or bad request: an unusable attempt; the next one gets the same request.
			l.progress()
			continue
		default:
			return l.fail(err, "extractor")
		}

		candidate, perr := parseJSON(resp.Text, allowArray)
		if perr != nil {
			what := "a single JSON object"
			if allowArray {
				what = "a single JSON object or array"
			}
			l.prev = nil
			msg := fmt.Sprintf("The answer was not %s: %s.", what, perr)
			if resp.Truncated {
				msg = fmt.Sprintf("The answer was cut off at the output-token limit, so it was not %s. "+
					"Answer again more compactly: no prose, no repetition, only the JSON.", what)
			}
			l.next = []Feedback{schemaFeedback("", msg)}
			if !l.usable {
				l.feedback = l.next // nothing parseable yet: report on the whole (null) result
			}
			l.progress()
			continue
		}
		l.usable, l.result, l.feedback, l.prev = true, candidate, nil, candidate

		if !schemaless {
			if violations := l.in.Schema.Validate(candidate); len(violations) > 0 {
				l.feedback = l.capped(violations)
				l.next = l.feedback
				l.progress()
				continue // no judge call
			}
		}

		v, err := l.verdict(ctx, at(l.roles.Judge, l.roles.JudgeEscalation, l.attempts), judgeSystem, candidate)
		if err != nil {
			return l.fail(err, "judge")
		}
		l.confidence = v.confidence
		if v.accept && l.roles.SecondJudge != nil {
			// A second, independent opinion: its accept confirms, its reject feeds the
			// next attempt, its failure fails the job as the judge's would.
			second, err := l.verdict(ctx, l.roles.SecondJudge, judgeSystem, candidate)
			if err != nil {
				return l.fail(err, "second_judge")
			}
			l.confidence = second.confidence
			if second.accept {
				second.feedback = slices.Concat(v.feedback, second.feedback) // both judges' advisory entries
			}
			v = second
		}
		if v.accept {
			l.feedback = v.feedback // advisory entries
			return l.outcome(StatusAccepted, nil)
		}
		l.feedback = v.feedback
		l.next = v.feedback
		l.progress()
	}

	if !l.usable {
		return l.failed(CodeExtractorFailed)
	}
	return l.outcome(StatusRejected, nil)
}

// errJudgeFailed: the judge's answer could not be parsed on any try.
var errJudgeFailed = errors.New("extract: verdict not parseable")

// verdict asks group for a verdict on candidate. A reject answered by a classifier
// endpoint, with feedback_fallback: llm, gets its feedback from the feedback group: the
// verdict stays reject whatever that group answers, and the classifier's own entry is
// kept when the group does not reject or fails (except on the job's deadline).
func (l *loop) verdict(ctx context.Context, g *router.Group, system string, candidate json.RawMessage) (verdict, error) {
	v, served, err := l.judge(ctx, g, system, candidate)
	if err != nil || v.accept || l.roles.FeedbackJudge == nil || !l.roles.Classifiers[served.Endpoint] {
		return v, err
	}
	fb, _, err := l.judge(ctx, l.roles.FeedbackJudge, system, candidate)
	switch {
	case errors.Is(err, errTimeout):
		return verdict{}, err
	case err != nil:
		slog.Warn("extract: feedback group failed; keeping the classifier's feedback", "error", err)
	case !fb.accept:
		v.feedback = fb.feedback
	}
	return v, nil
}

// judge asks group for a verdict on candidate, once more with a format reminder per
// reparse try when the answer cannot be parsed. A permanent failure (a refusal) counts
// as an unparseable answer. It returns the pair that gave the verdict.
func (l *loop) judge(ctx context.Context, g *router.Group, system string, candidate json.RawMessage) (verdict, router.Served, error) {
	for try := 0; try <= l.roles.JudgeReparseTries; try++ {
		resp, served, err := l.call(ctx, g, system, assembleJudge(l.in, candidate, try > 0))
		if errors.Is(err, router.ErrPermanent) {
			continue
		}
		if err != nil {
			return verdict{}, router.Served{}, err
		}
		v, err := parseVerdict(resp.Text, l.maxChars)
		if err != nil {
			continue
		}
		if v.ignoredConfidence {
			slog.Warn("extract: judge confidence ignored", "reason", "not a number from 0 to 1")
		}
		return v, served, nil
	}
	return verdict{}, router.Served{}, errJudgeFailed
}

// call makes one role call. The job's deadline (or cancellation) is checked first,
// because the router reports an exhausted group even when a per-try timeout was the
// cause.
func (l *loop) call(ctx context.Context, g *router.Group, system string, parts []provider.Part) (provider.Response, router.Served, error) {
	resp, served, err := g.Complete(ctx, provider.Request{System: system, Parts: parts})
	switch {
	case err == nil:
		return resp, served, nil
	case ctx.Err() != nil:
		return provider.Response{}, router.Served{}, errTimeout
	case errors.Is(err, router.ErrGroupUnavailable):
		return provider.Response{}, router.Served{}, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	return provider.Response{}, router.Served{}, err
}

// fail maps a role call error to the failed outcome.
func (l *loop) fail(err error, role string) Outcome {
	switch {
	case errors.Is(err, errTimeout):
		return l.failed(CodeTimeout)
	case errors.Is(err, errUnavailable):
		slog.Warn("extract: endpoint group unavailable", "role", role, "error", err)
		return l.failed(CodeProviderUnavailable)
	case errors.Is(err, errJudgeFailed):
		return l.failed(CodeJudgeFailed)
	}
	slog.Error("extract: unexpected role call error", "role", role, "error", err)
	return l.failed(CodeInternal)
}

var jobErrorMessages = map[string]string{
	CodeProviderUnavailable: "Every endpoint of a role's group failed.",
	CodeExtractorFailed:     "The extractor gave no usable answer on any attempt.",
	CodeJudgeFailed:         "The judge's verdict could not be parsed.",
	CodeTimeout:             "The job's time limit expired.",
	CodeInternal:            "Internal error.",
}

func (l *loop) failed(code string) Outcome {
	return l.outcome(StatusFailed, &JobError{Code: code, Message: jobErrorMessages[code]})
}

func (l *loop) outcome(status Status, jobErr *JobError) Outcome {
	feedback := l.feedback
	if feedback == nil {
		feedback = []Feedback{}
	}
	return Outcome{
		Status:          status,
		Result:          l.result,
		Feedback:        feedback,
		Attempts:        l.attempts,
		JudgeConfidence: l.confidence,
		Error:           jobErr,
	}
}

func (l *loop) progress() {
	if l.in.OnAttempt != nil {
		l.in.OnAttempt(Progress{Attempts: l.attempts, Result: l.result, Feedback: l.feedback})
	}
}

func (l *loop) capped(feedback []Feedback) []Feedback {
	for i := range feedback {
		feedback[i].Message = capMessage(feedback[i].Message, l.maxChars)
	}
	return feedback
}
