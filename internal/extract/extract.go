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

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/ground"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
)

// Status is a job's terminal status (contract §5).
type Status string

// Terminal statuses. Only StatusAccepted is acceptance. StatusExtracted (judge policy
// never, ADR 0009) is a schema-valid final candidate no judge looked at: never
// acceptance, and outside contract v1, whose clients treat it as not accepted.
const (
	StatusAccepted  Status = "accepted"
	StatusRejected  Status = "rejected"
	StatusFailed    Status = "failed"
	StatusExtracted Status = "extracted"
)

// Verifications: what made a terminal job's outcome (Outcome.Verification, the job's
// verification field). With judge policy always, accepted pairs with VerificationJudge
// and every other status with VerificationNone.
const (
	VerificationJudge    = "judge"    // the judge accepted
	VerificationGrounded = "grounded" // the grounding check accepted without a judge (auto)
	VerificationNone     = "none"     // nothing accepted the result
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
	// SourceGrounding marks an entry of the grounding check (ADR 0009): a value not found
	// in the document text. In the job's feedback only under judge policy never or auto.
	SourceGrounding = ground.Source
)

// Judge decisions (Outcome.Decision): how the last schema-valid candidate of a job was
// decided, for vde_judge_decisions_total.
const (
	DecisionJudged   = "judged"   // the judge was asked (always, or auto when the gate failed)
	DecisionGrounded = "grounded" // auto: the grounding gate accepted without a judge
	DecisionSampled  = "sampled"  // auto: the gate passed, and the sampled judge was asked anyway
	DecisionSkipped  = "skipped"  // never: no judge
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
	// PriorRepaired is Progress.Repaired as persisted: the job already made its one
	// grounding repair (ADR 0009), which its saved feedback no longer shows once a judge
	// answered after it.
	PriorRepaired bool
	// JudgePolicy is the job's effective judge policy (request, else application, else
	// roles.judge.policy); "" means config.JudgeAlways. The grounding check reads
	// Representation.GroundText; under never and auto the caller fills it for PDFs sent
	// natively or as pages (document.GroundingText), under always it does not.
	JudgePolicy config.JudgePolicy
	// SampleRate (auto) is the share of grounded jobs judged anyway, and SampleKey the
	// bytes it is decided on (Sampled): the job id in the service, the document bytes in
	// the CLI. A resumed job keeps its key, so its sampling decision.
	SampleRate float64
	SampleKey  []byte
	// Deadline is when the job must be terminal (created_at plus limits.job_timeout);
	// the zero time adds no deadline to ctx.
	Deadline time.Time
	// OnAttempt, when set, is called after each attempt that sends the loop on to
	// another attempt or to the attempt limit, so the caller can persist progress.
	OnAttempt func(Progress)
	// Logger receives the loop's warnings and errors (error chains and a panic's stack,
	// never content). Callers pass the redacting logger of internal/logging; nil means
	// slog.Default().
	Logger *slog.Logger
}

// Progress is the state after an attempt.
type Progress struct {
	Attempts int
	Result   json.RawMessage // the last parseable candidate, nil when none yet
	Feedback []Feedback      // the feedback on Result, or on an answer that was not JSON
	Usage    Usage           // of this Run so far (not of Input.PriorAttempts)
	// Repaired reports that the job made its one grounding repair; persist it and pass
	// it back as Input.PriorRepaired on resume.
	Repaired bool
}

// Usage is the provider token usage of the role calls of one Run, as the providers
// report it: every call that answered, judge reparse tries, the second judge and the
// feedback group included. A call that failed reports no tokens.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	Calls        int `json:"calls"` // role calls that answered
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
	// Verification is VerificationJudge, VerificationGrounded or VerificationNone.
	Verification string `json:"verification"`
	// Usage is the token usage of this Run (metadata, not part of the job resource).
	Usage Usage `json:"-"`
	// JudgeCalls counts the judge-role calls of this Run that answered: judge, re-asks,
	// second judge and feedback group (metadata, for the evaluation report).
	JudgeCalls int `json:"-"`
	// Decision is how the last schema-valid candidate was decided (Decision*), "" when no
	// candidate reached the decision; Disagreements counts sampled judge calls that
	// rejected a grounded candidate. Both are metadata for the metrics.
	Decision      string `json:"-"`
	Disagreements int    `json:"-"`
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
	l := &loop{in: &in, roles: roles, maxChars: roles.FeedbackMessageChars, policy: in.JudgePolicy, log: in.Logger}
	if l.log == nil {
		l.log = slog.Default()
	}
	if l.policy == "" {
		l.policy = config.JudgeAlways
	}
	if l.maxChars <= 0 {
		l.maxChars = DefaultFeedbackMessageChars
	}
	defer func() {
		if r := recover(); r != nil {
			attrs := []any{"stack", string(debug.Stack())}
			if re, ok := r.(runtime.Error); ok {
				attrs = append(attrs, "panic", re.Error())
			}
			l.log.Error("extract: panic recovered", attrs...)
			out = l.failed(CodeInternal)
		}
	}()
	if roles.Extractor == nil || roles.Judge == nil {
		l.log.Error("extract: roles without endpoint groups")
		return l.failed(CodeInternal)
	}
	return l.run(ctx)
}

// loop is the state of one Run.
type loop struct {
	in       *Input
	roles    Roles
	maxChars int
	log      *slog.Logger // Input.Logger or slog.Default()

	maxAttempts     int    // Input.MaxAttempts or DefaultMaxAttempts
	extractorSystem string // the role framings (schema or schema-less)
	judgeSystem     string
	allowArray      bool // a top-level array answer is accepted

	attempts   int
	usable     bool            // some attempt produced parseable JSON
	result     json.RawMessage // the last parseable candidate
	feedback   []Feedback      // the feedback on result
	prev       json.RawMessage // the candidate for the next extractor request
	next       []Feedback      // the feedback for the next extractor request
	confidence *float64        // of the last parsed verdict
	usage      Usage

	policy        config.JudgePolicy
	doc           *ground.Text // the grounding index, built on first use
	repaired      bool         // the one grounding repair of the job was made
	judgeCalls    int
	decision      string
	disagreements int
}

// run is the loop of contract §6: up to the attempt limit, one extractor attempt, then,
// for a schema-valid candidate, the decision by the judge policy. Every attempt that
// does not end the job goes on to the next one.
func (l *loop) run(ctx context.Context) Outcome {
	l.resume()
	for l.attempts < l.maxAttempts {
		if ctx.Err() != nil {
			return l.failed(CodeTimeout)
		}
		l.attempts++
		candidate, err := l.extractOnce(ctx)
		if err != nil {
			return l.fail(err, "extractor")
		}
		if candidate == nil {
			continue
		}
		if out, done := l.decide(ctx, candidate); done {
			return out
		}
	}
	if !l.usable {
		return l.failed(CodeExtractorFailed)
	}
	return l.outcome(StatusRejected, nil)
}

// resume sets the loop's limits and framings and takes over the state of an earlier run
// of the job (Input.Prior*).
func (l *loop) resume() {
	l.maxAttempts = l.in.MaxAttempts
	if l.maxAttempts <= 0 {
		l.maxAttempts = DefaultMaxAttempts
	}
	l.attempts = l.in.PriorAttempts
	if l.in.PriorCandidate != nil {
		l.usable, l.result, l.prev = true, l.in.PriorCandidate, l.in.PriorCandidate
	}
	l.feedback, l.next = l.in.PriorFeedback, l.in.PriorFeedback
	// Feedback with grounding entries also marks a repair (a row persisted before the
	// flag existed).
	l.repaired = l.in.PriorRepaired ||
		slices.ContainsFunc(l.in.PriorFeedback, func(f Feedback) bool { return f.Source == SourceGrounding })
	schemaless := l.in.Schema == nil
	l.extractorSystem, l.judgeSystem = framings(schemaless)
	l.allowArray = schemaless || l.in.Schema.allowArray
}

// extractOnce makes one extractor attempt and returns its candidate when it parses and
// validates against the schema (when given). Otherwise the candidate is nil, the
// feedback for the next attempt is set and progress reported; the error is a role call
// error that ends the job.
func (l *loop) extractOnce(ctx context.Context) (json.RawMessage, error) {
	extractor := at(l.roles.Extractor, l.roles.ExtractorEscalation, l.attempts)
	resp, _, err := l.call(ctx, extractor, l.extractorSystem, assembleExtractor(l.in, l.prev, l.next))
	switch {
	case err == nil:
	case errors.Is(err, router.ErrPermanent):
		// A refusal or bad request: an unusable attempt; the next one gets the same request.
		l.progress()
		return nil, nil
	default:
		return nil, err
	}

	candidate, perr := parseJSON(resp.Text, l.allowArray)
	if perr != nil {
		l.notJSON(perr, resp.Truncated)
		return nil, nil
	}
	l.usable, l.result, l.feedback, l.prev = true, candidate, nil, candidate

	if l.in.Schema != nil {
		if violations := l.in.Schema.Validate(candidate); len(violations) > 0 {
			l.feedback = l.capped(violations)
			l.next = l.feedback
			l.progress()
			return nil, nil // no judge call
		}
	}
	return candidate, nil
}

// notJSON sets the feedback on an answer that did not parse (perr), or that the output
// token limit cut off.
func (l *loop) notJSON(perr error, truncated bool) {
	what := "a single JSON object"
	if l.allowArray {
		what = "a single JSON object or array"
	}
	l.prev = nil
	msg := fmt.Sprintf("The answer was not %s: %s.", what, perr)
	if truncated {
		msg = fmt.Sprintf("The answer was cut off at the output-token limit, so it was not %s. "+
			"Answer again more compactly: one short analysis line per value, no repetition, then the JSON block.", what)
	}
	l.next = []Feedback{schemaFeedback("", msg)}
	if !l.usable {
		l.feedback = l.next // nothing parseable yet: report on the whole (null) result
	}
	l.progress()
}

// decide decides a schema-valid candidate by the judge policy (ADR 0009): grounding, the
// one grounding repair, the auto gate and its sample, then the judge. done reports a
// terminal outcome; otherwise the feedback for the next attempt is set and progress
// reported. A failed judge call fails the job, never accepts.
func (l *loop) decide(ctx context.Context, candidate json.RawMessage) (out Outcome, done bool) {
	grounded, misses := l.ground(candidate)
	if l.policy == config.JudgeNever {
		l.decision, l.feedback = DecisionSkipped, misses // advisory
		return l.outcome(StatusExtracted, nil), true
	}
	// The one grounding repair: back to the extractor, no judge call. Under always only
	// for a schema with an x-vde-grounding annotation: an unannotated schema has not
	// said which values are derivations (a city in English, an IATA code), so its
	// misses are mostly legitimate and the judge decides without the extra call (ADR
	// 0009, amendment 2026-10-18). Under auto every miss is repaired.
	repairable := l.policy == config.JudgeAuto || l.in.Schema.GroundingAnnotated()
	if grounded.CriticalMissed > 0 && repairable && !l.repaired && l.attempts < l.maxAttempts {
		l.repaired = true
		l.feedback, l.next = misses, misses
		l.progress()
		return Outcome{}, false
	}
	// The auto gate: grounded text, at least one critical leaf checked and every one
	// grounded (a candidate with nothing to check, such as {} or only nulls, goes to the
	// judge), and a candidate that does not answer schema or judge feedback (a judge's
	// rejection is not overruled). l.next is still the feedback the candidate was asked
	// to answer.
	answersFeedback := slices.ContainsFunc(l.next, func(f Feedback) bool { return f.Source != SourceGrounding })
	gate := l.policy == config.JudgeAuto && grounded.CriticalOK() && !answersFeedback
	sampled := gate && Sampled(l.in.SampleKey, l.in.SampleRate)
	switch {
	case gate && !sampled:
		l.decision, l.feedback, l.confidence = DecisionGrounded, nil, nil
		return l.outcome(StatusAccepted, nil), true
	case sampled:
		l.decision = DecisionSampled
	case l.decision != DecisionSampled: // a sampled job stays sampled for the metrics
		l.decision = DecisionJudged
	}
	if l.policy != config.JudgeAuto {
		misses = nil // under always, grounding entries reach only the extractor
	}

	v, role, err := l.judged(ctx, candidate)
	if err != nil {
		return l.fail(err, role), true
	}
	if v.accept {
		l.feedback = slices.Concat(v.feedback, misses) // advisory entries
		return l.outcome(StatusAccepted, nil), true
	}
	if sampled {
		l.disagreements++ // the judge rejected what grounding alone would have accepted
	}
	l.feedback = slices.Concat(v.feedback, misses)
	l.next = l.feedback
	l.progress()
	return Outcome{}, false
}

// judged asks the judge for a verdict on candidate and, when it accepts and a second
// judge is configured, the second judge too: its accept confirms (with both judges'
// advisory entries), its reject is the verdict. An error names the role that failed.
func (l *loop) judged(ctx context.Context, candidate json.RawMessage) (verdict, string, error) {
	v, err := l.verdict(ctx, at(l.roles.Judge, l.roles.JudgeEscalation, l.attempts), l.judgeSystem, candidate)
	if err != nil {
		return verdict{}, "judge", err
	}
	l.confidence = v.confidence
	if !v.accept || l.roles.SecondJudge == nil {
		return v, "", nil
	}
	second, err := l.verdict(ctx, l.roles.SecondJudge, l.judgeSystem, candidate)
	if err != nil {
		return verdict{}, "second_judge", err
	}
	l.confidence = second.confidence
	if second.accept {
		second.feedback = slices.Concat(v.feedback, second.feedback) // both judges' advisory entries
	}
	return second, "", nil
}

// ground checks a schema-valid candidate against the document text, when the
// representation has any, and returns the result with its feedback entries (nil when
// every leaf is grounded or there is no text: the result is then Ungroundable).
func (l *loop) ground(candidate json.RawMessage) (ground.Result, []Feedback) {
	rep := l.in.Representation
	if !rep.Groundable {
		return ground.Result{Ungroundable: true}, nil
	}
	if l.doc == nil {
		l.doc = ground.NewText(rep.GroundText)
	}
	res := ground.Check(l.doc, candidate, l.in.Schema.GroundingRules(), maxSchemaFeedback)
	var out []Feedback
	for _, m := range res.Ungrounded {
		out = append(out, Feedback{Pointer: m.Pointer, Message: ground.Message, Source: SourceGrounding})
	}
	return res, out
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
		l.log.Warn("extract: feedback group failed; keeping the classifier's feedback", "error", err)
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
		l.judgeCalls++
		v, err := parseVerdict(resp.Text, l.maxChars)
		if err != nil {
			continue
		}
		if v.ignoredConfidence {
			l.log.Warn("extract: judge confidence ignored", "reason", "not a number from 0 to 1")
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
		l.usage.Calls++
		l.usage.InputTokens += resp.InputTokens
		l.usage.OutputTokens += resp.OutputTokens
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
		l.log.Warn("extract: endpoint group unavailable", "role", role, "error", err)
		return l.failed(CodeProviderUnavailable)
	case errors.Is(err, errJudgeFailed):
		return l.failed(CodeJudgeFailed)
	}
	l.log.Error("extract: unexpected role call error", "role", role, "error", err)
	return l.failed(CodeInternal)
}

// jobErrorMessages are the job error messages of every code, generic: NewJobError.
var jobErrorMessages = map[string]string{
	CodeProviderUnavailable: "Every endpoint of a role's group failed.",
	CodeExtractorFailed:     "The extractor gave no usable answer on any attempt.",
	CodeJudgeFailed:         "The judge's verdict could not be parsed.",
	CodeTimeout:             "The job's time limit expired.",
	CodeInternal:            "Internal error.",
}

// NewJobError returns the error of a job failed with code, with its fixed message.
func NewJobError(code string) *JobError {
	return &JobError{Code: code, Message: jobErrorMessages[code]}
}

func (l *loop) failed(code string) Outcome {
	return l.outcome(StatusFailed, NewJobError(code))
}

func (l *loop) outcome(status Status, jobErr *JobError) Outcome {
	feedback := visibleFeedback(l.policy, l.feedback)
	if feedback == nil {
		feedback = []Feedback{}
	}
	verification := VerificationNone
	if status == StatusAccepted {
		verification = VerificationJudge // a judge's accept, or the auto grounding gate
		if l.decision == DecisionGrounded {
			verification = VerificationGrounded
		}
	}
	return Outcome{
		Status:          status,
		Result:          l.result,
		Feedback:        feedback,
		Attempts:        l.attempts,
		JudgeConfidence: l.confidence,
		Error:           jobErr,
		Verification:    verification,
		Usage:           l.usage,
		JudgeCalls:      l.judgeCalls,
		Decision:        l.decision,
		Disagreements:   l.disagreements,
	}
}

// visibleFeedback is feedback as the job shows it under policy: under always (contract v1
// sources only) a grounding repair's entries reach only the extractor, never the job's
// feedback. feedback itself is never modified.
func visibleFeedback(policy config.JudgePolicy, feedback []Feedback) []Feedback {
	if (policy != "" && policy != config.JudgeAlways) || !slices.ContainsFunc(feedback, isGrounding) {
		return feedback
	}
	return slices.DeleteFunc(slices.Clone(feedback), isGrounding)
}

func isGrounding(f Feedback) bool { return f.Source == SourceGrounding }

// VisibleFeedback is a job row's persisted feedback (a JSON array) as clients may see it
// under the row's judge policy ("" means always): every place that shows or finalizes
// saved feedback (GET while running, the worker's and the sweeper's terminal writes
// without a loop outcome) drops a grounding repair's entries under always, as the loop
// does. Feedback that does not parse, or has nothing to drop, is returned unchanged.
func VisibleFeedback(policy string, feedback json.RawMessage) json.RawMessage {
	var entries []Feedback
	if len(feedback) == 0 || json.Unmarshal(feedback, &entries) != nil {
		return feedback
	}
	visible := visibleFeedback(config.JudgePolicy(policy), entries)
	if len(visible) == len(entries) {
		return feedback
	}
	b, err := json.Marshal(visible)
	if err != nil {
		return feedback
	}
	return b
}

func (l *loop) progress() {
	if l.in.OnAttempt != nil {
		l.in.OnAttempt(Progress{Attempts: l.attempts, Result: l.result, Feedback: l.feedback, Usage: l.usage, Repaired: l.repaired})
	}
}

func (l *loop) capped(feedback []Feedback) []Feedback {
	for i := range feedback {
		feedback[i].Message = capMessage(feedback[i].Message, l.maxChars)
	}
	return feedback
}
