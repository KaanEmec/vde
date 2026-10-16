package logging

import (
	"io"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"
)

// Value length limits, in runes.
const (
	// MaxValueLen is the longest string value that is logged whole.
	MaxValueLen = 256
	// MaxStackLen is the limit for the "stack" key: a goroutine stack names code, never
	// content, and is useless when cut at 256.
	MaxStackLen = 4096

	truncated = "...(truncated)"
)

// allowed is the fixed vocabulary of attribute keys. Adding a key is a deliberate
// act: it must never carry document content, instructions, schemas, candidates,
// feedback, provider response bodies or credentials.
var allowed = []string{
	// Identity and correlation.
	"job_id", "app_id", "application_id", "request_id", "worker_id",
	// Job state.
	"status", "attempts", "error_code", "outcome", "kind", "role", "count",
	// Provider routing: ids from configuration only.
	"endpoint", "model",
	// Request and document metadata, never content.
	"method", "route", "media_type", "bytes", "pages", "duration_ms",
	// Process.
	"service_version", "version", "component", "listen", "tls", "trusted_proxy",
	"drain_timeout",
	// Diagnostics. "error" and "reason" carry messages the code controls (provider
	// errors never quote content, contract §2); "panic" and "stack" name code.
	"error", "reason", "panic", "stack",
	// Audit lines (audit.log): details is JSON built from the audit package's fixed
	// structs, never content.
	"audit", "actor", "details",
	// The standard slog keys.
	slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey,
}

// AllowedKeys returns the attribute keys the redacting handler lets through, sorted.
// The result is a copy.
func AllowedKeys() []string {
	keys := slices.Clone(allowed)
	slices.Sort(keys)
	return keys
}

var allowSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(allowed))
	for _, k := range allowed {
		m[k] = struct{}{}
	}
	return m
}()

// New returns a logger writing to w at level and format ("json" or "text"; anything
// else is JSON). Every attribute whose key is not in AllowedKeys is dropped, and string
// values longer than MaxValueLen runes are cut.
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redact}
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// redact is the slog.HandlerOptions.ReplaceAttr hook. It sees leaf attributes only;
// groups are flattened by their members' keys.
func redact(_ []string, a slog.Attr) slog.Attr {
	if _, ok := allowSet[a.Key]; !ok {
		return slog.Attr{} // an empty Attr is omitted
	}
	v := a.Value.Resolve()
	limit := MaxValueLen
	if a.Key == "stack" {
		limit = MaxStackLen
	}
	switch v.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(truncate(v.String(), limit))
	case slog.KindAny:
		// Errors and other values are rendered by the handler with their own methods,
		// which the limit could not reach; render them here.
		if err, ok := v.Any().(error); ok && err != nil {
			a.Value = slog.StringValue(truncate(err.Error(), limit))
		} else if s, ok := v.Any().(interface{ String() string }); ok {
			a.Value = slog.StringValue(truncate(s.String(), limit))
		} else {
			// Unknown shapes (structs, maps, slices) could hold anything.
			return slog.Attr{}
		}
	}
	return a
}

// truncate cuts s to at most limit runes plus a marker, keeping valid UTF-8.
func truncate(s string, limit int) string {
	if len(s) <= limit { // fewer bytes than the limit means fewer runes
		return s
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == limit {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString(truncated)
	return b.String()
}
