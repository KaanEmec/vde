// Package extract runs the bounded extraction loop of the contract (§6, §7, §9): the
// fixed role framings, lenient JSON answer parsing, schema validation turned into
// feedback, the judge verdict and the mapping of every failure to a job error code. It
// is usable without the HTTP layer, from the vde extract command and from tests.
//
// Wiring, once per process:
//
//	roles, err := extract.NewRoles(cfg, build) // groups of roles.extractor and roles.judge
//	version := extract.ServiceVersion(cfg, version.Version)
//
// and per job:
//
//	schema, err := extract.CompileSchema(schemaBytes) // errors.Is(err, extract.ErrInvalidSchema)
//	rep, err := document.Select(doc, cfg.Capabilities(extractorGroup), cfg.Capabilities(judgeGroup), policy)
//	out := extract.Run(ctx, extract.Input{
//		Representation:        rep,
//		ExtractorInstructions: extractorInstructions,
//		JudgeInstructions:     judgeInstructions,
//		Schema:                schema, // nil for schema-less extraction
//		MaxAttempts:           cfg.Limits.MaxAttempts,
//		Deadline:              createdAt.Add(cfg.Limits.JobTimeout),
//	}, roles)
//
// Run never returns an error: every outcome, including provider failures, timeouts and
// panics, is an Outcome with a terminal status. Nothing it logs carries content.
package extract
