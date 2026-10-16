// Package fake is a scripted provider.Provider for tests: it replays answers, errors and
// delays in order and records every request so tests can assert what each role saw.
//
// A Script is a list of steps. Each call takes the first unconsumed step that matches
// the request (Role is a substring of Request.System, Model equals Request.Model; empty
// fields match anything), waits its Delay, then answers its text or returns its error.
// A step with Repeat is never consumed. A call no step matches fails permanently with
// ErrExhausted. The fake has no build tag and no secrets, so it can back a `provider:
// fake` endpoint in any build.
//
// Script files (Load, Parse) are YAML, so scenarios in testdata/scenarios replay without
// Go code:
//
//	description: primary down, fallback answers   # optional, free text
//	steps:
//	  - model: primary-model                      # optional: match Request.Model
//	    repeat: true                              # optional: never consumed
//	    error: {kind: retryable, status: 503, retry_after: 2s}
//	  - role: extractor                           # optional: substring of Request.System
//	    delay: 50ms                               # optional: wait first (honours ctx)
//	    answer: '{"name": "Ada"}'
//
// Every step has exactly one of answer (may be "") or error. error.kind is retryable or
// permanent; status and retry_after are optional. Unknown keys are rejected.
package fake
