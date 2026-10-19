// Package logging builds the service's slog loggers. The routine logger keeps a fixed
// vocabulary of attribute keys and truncates long values, so a future logging mistake
// cannot put document bytes, instructions, schemas, candidates, feedback or provider
// error bodies into the log (contract §10).
package logging
