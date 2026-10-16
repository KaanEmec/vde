// Package router serves a role from its endpoint group: ordered failover across
// endpoint-model pairs, bounded tries and total time, and per-endpoint health marking.
// It never fails over across roles: a Group knows only its own pairs.
//
// # Failover
//
// Group.Complete skips pairs whose accepts list lacks a media type of the request's image
// or document parts, then tries the rest in configured order, pairs marked down last.
// Each try gets the endpoint's timeout; the whole call is bounded by failover.max_tries,
// failover.max_total_time and the caller's deadline, whichever ends first. Tries are
// separated by a 250 ms wait with ±50% jitter. A retryable failure moves to the next pair,
// except that a Retry-After of at most 10 s that fits in the remaining time is waited out
// and the same pair retried once. No provider is called once the context is done.
//
// # Errors
//
// Check them in this order; an ErrGroupUnavailable can wrap a per-try deadline.
//
//	Returned error                        Cause                                        Job error code
//	errors.Is(err, ErrGroupUnavailable)   every eligible pair failed retryably, or     provider_unavailable
//	                                      max_tries or max_total_time ran out
//	errors.Is(err, ErrPermanent)          a pair failed permanently (refusal, bad      extractor_failed or
//	                                      request) without failover_on_permanent, or   judge_failed
//	                                      no pair accepts the request's media types
//	ctx.Err(), unchanged                  the caller's deadline or cancellation        timeout
//
// Both sentinels wrap the last pair's error, so errors.As finds its *provider.Error.
//
// # Health
//
// A retryable failure marks a pair down for 30 s, doubling per consecutive failed window
// up to 5 m (or longer up to that cap when Retry-After asks), and a success clears it.
// Marked-down pairs are still tried after the healthy ones. State is in memory, per
// process. Group.Healthy reports whether any pair is not marked down.
package router
