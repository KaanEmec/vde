// Package webhook delivers completion webhooks: when a job of an application with
// applications[].webhook reaches its terminal state, a signed POST with the job's id,
// status, service_version and finished_at (never its result or feedback) goes to the
// operator-approved URL, retried with backoff for a bounded time. Deliveries live in
// memory: a restart drops the pending ones, so callers keep polling as the fallback.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/job"
)

// Headers of a delivery.
const (
	HeaderSignature = "X-VDE-Signature" // t=<unix seconds>,v1=<hex HMAC-SHA256 of "<t>.<body>">
	HeaderEvent     = "X-VDE-Event"
)

// EventJobFinished is the only event.
const EventJobFinished = "job.finished"

// DefaultBackoff is the wait before each retry: five attempts over about 15 minutes.
var DefaultBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 5 * time.Minute, 8 * time.Minute}

const (
	defaultAttemptTimeout = 10 * time.Second
	defaultMaxPending     = 1000
)

// Payload is the body of a job.finished delivery. It never carries content: the
// receiver fetches GET /v1/jobs/{id}, which keeps the retention rules intact.
type Payload struct {
	Event          string `json:"event"`
	JobID          string `json:"job_id"`
	ApplicationID  string `json:"application_id"`
	Status         string `json:"status"`
	ServiceVersion string `json:"service_version"`
	FinishedAt     string `json:"finished_at"` // RFC 3339 UTC, whole seconds
}

// Target is one application's webhook.
type Target struct {
	URL    string
	Secret []byte
}

// Options configure a Dispatcher. Zero values take the defaults.
type Options struct {
	Backoff        []time.Duration // waits between attempts; DefaultBackoff
	AttemptTimeout time.Duration   // per attempt
	MaxPending     int             // deliveries waiting or in flight; more are dropped and audited
	Client         *http.Client    // never follows redirects; a default client when nil
	Audit          *audit.Recorder // receives webhook.delivered and webhook.failed
	Logger         *slog.Logger
	Now            func() time.Time // the signature timestamp; time.Now
}

// Dispatcher sends the webhooks of the applications it was given. Each pending delivery
// is one goroutine that sleeps between attempts; Stop cancels them.
type Dispatcher struct {
	targets map[string]Target
	opts    Options
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	pending int
	stopped bool
}

// New returns a dispatcher for targets, keyed by application id.
func New(targets map[string]Target, opts Options) *Dispatcher {
	if opts.Backoff == nil {
		opts.Backoff = DefaultBackoff
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = defaultAttemptTimeout
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = defaultMaxPending
	}
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	client := *opts.Client // the receiver's redirects are not followed
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	opts.Client = &client
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.Audit = opts.Audit.WithActor(audit.ActorWebhook)
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{targets: targets, opts: opts, ctx: ctx, cancel: cancel}
}

// JobFinished queues the job.finished delivery of j when its application has a
// webhook. It never blocks; it is the worker pool's completion hook.
func (d *Dispatcher) JobFinished(j job.Job) {
	if d == nil {
		return
	}
	t, ok := d.targets[j.ApplicationID]
	if !ok {
		return
	}
	body, _ := json.Marshal(Payload{ // plain strings: cannot fail
		Event: EventJobFinished, JobID: j.ID, ApplicationID: j.ApplicationID, Status: string(j.Status),
		ServiceVersion: j.ServiceVersion, FinishedAt: j.FinishedAt.UTC().Truncate(time.Second).Format(time.RFC3339),
	})
	d.mu.Lock()
	full := d.pending >= d.opts.MaxPending
	if !d.stopped && !full {
		d.pending++
		d.wg.Add(1)
	}
	stopped := d.stopped
	d.mu.Unlock()
	switch {
	case stopped:
		return
	case full:
		d.record(j, audit.WebhookFailed{Event: EventJobFinished, Reason: audit.WebhookQueueFull, Final: true})
		return
	}
	go func() {
		defer func() {
			d.mu.Lock()
			d.pending--
			d.mu.Unlock()
			d.wg.Done()
		}()
		d.deliver(j, t, body)
	}()
}

// Stop cancels the pending deliveries and waits until their goroutines returned or ctx
// is done. Deliveries not made by then are lost (callers poll).
func (d *Dispatcher) Stop(ctx context.Context) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	d.stopped = true
	d.mu.Unlock()
	d.cancel()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deliver makes up to len(Backoff)+1 attempts, waiting the backoff between them.
func (d *Dispatcher) deliver(j job.Job, t Target, body []byte) {
	attempts := len(d.opts.Backoff) + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		status, err := d.attempt(t, body)
		if err == nil && status/100 == 2 {
			d.record(j, audit.WebhookDelivered{Event: EventJobFinished, Attempt: attempt, StatusCode: status})
			return
		}
		failed := audit.WebhookFailed{Event: EventJobFinished, Attempt: attempt, Reason: audit.WebhookStatus,
			StatusCode: status, Final: attempt == attempts}
		if err != nil {
			failed.Reason, failed.StatusCode = audit.WebhookNetwork, 0
		}
		if d.ctx.Err() != nil { // stopping: this attempt was cut short, none follows
			failed.Final = true
		}
		d.record(j, failed)
		if failed.Final {
			return
		}
		timer := time.NewTimer(d.opts.Backoff[attempt-1])
		select {
		case <-d.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// attempt POSTs body once and returns the status.
func (d *Dispatcher) attempt(t Target, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(d.ctx, d.opts.AttemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "vde-webhook")
	req.Header.Set(HeaderEvent, EventJobFinished)
	req.Header.Set(HeaderSignature, Sign(t.Secret, d.opts.Now(), body))
	resp, err := d.opts.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // reuse the connection
	return resp.StatusCode, nil
}

func (d *Dispatcher) record(j job.Job, details audit.Details) {
	if err := d.opts.Audit.Record(context.WithoutCancel(d.ctx), "", j.ApplicationID, j.ID, details); err != nil {
		d.opts.Logger.Warn("audit event not written", "kind", string(details.AuditKind()), "job_id", j.ID, "error", err)
	}
	if f, ok := details.(audit.WebhookFailed); ok && f.Final {
		d.opts.Logger.Warn("webhook not delivered", "job_id", j.ID, "application_id", j.ApplicationID,
			"attempts", f.Attempt, "reason", f.Reason)
	}
}

// Sign returns the X-VDE-Signature value for body sent at t: "t=<unix>,v1=<hex>", the
// HMAC-SHA256 under secret of "<unix>.<body>".
func Sign(secret []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac(secret, ts, body))
}

// ErrBadSignature: Verify refused a delivery.
var ErrBadSignature = errors.New("webhook: signature missing, malformed, stale or wrong")

// Verify checks a signature header against body, refusing a timestamp more than
// tolerance away from now (replay protection). Receivers written in Go can use it; the
// guide shows the same check in prose.
func Verify(secret []byte, header string, body []byte, now time.Time, tolerance time.Duration) error {
	var ts, sig string
	for field := range bytes.SplitSeq([]byte(header), []byte(",")) {
		k, v, _ := bytes.Cut(field, []byte("="))
		switch string(k) {
		case "t":
			ts = string(v)
		case "v1":
			sig = string(v)
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return ErrBadSignature
	}
	if age := now.Sub(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return ErrBadSignature
	}
	want := mac(secret, ts, body)
	got, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}

func mac(secret []byte, ts string, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts))
	m.Write([]byte{'.'})
	m.Write(body)
	return m.Sum(nil)
}
