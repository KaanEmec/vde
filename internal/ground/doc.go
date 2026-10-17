// Package ground is the deterministic grounding check of ADR 0009: every string and
// number leaf of a candidate must occur in the document's text. It makes no model calls,
// does no I/O and logs nothing; no value it reports carries a candidate value.
//
// Usage, once per job and once per candidate:
//
//	doc := ground.NewText(rep.GroundText)          // normalized index, built once
//	rules, err := ground.RulesFromSchema(rawSchema) // nil Rules for schema-less jobs
//	res := ground.Check(doc, candidate, rules, ground.Options{})
//	feedback := res.Ungrounded.Feedback()           // source "grounding", fixed message
//
// # Rules (RulesVersion)
//
//   - Text is normalized by Normalize: NFKD, combining marks dropped, casefolded (Turkish
//     dotted and dotless i become i, ß becomes ss), format characters such as soft
//     hyphens dropped, whitespace and control characters collapsed to one space, and
//     punctuation and symbols stripped at token edges but kept inside tokens (LX42QZ,
//     A-12, 10:45, 21.03.2027).
//   - A string leaf is grounded when its normalized form occurs in the normalized text on
//     word boundaries (the runes around the match are neither letters nor digits). A
//     string of two to six words is also grounded when all its words occur within a short
//     window in any order, so SMITH/JOHN grounds "John Smith".
//   - A number leaf, or a string that is a number with an optional currency symbol or
//     code, is grounded when the text prints a number within 0.005 of it. Printed numbers
//     accept the thousands separators '.', ',' and space (thin and no-break spaces
//     included) and both decimal conventions; an ambiguous form such as 1.234 counts as
//     both 1234 and 1.234. Signs are ignored. A digit run that follows a letter is not a
//     number (LX42 does not print 42).
//   - An ISO date (YYYY-MM-DD) is grounded when one of DateForms occurs, with month names
//     from Months. An ISO datetime needs its date and its time (TimeForms); a time-only
//     string (HH:MM[:SS]) needs its time. Seconds and zones are ignored.
//   - Booleans, nulls, empty strings, strings shorter than Options.MinLength and values
//     equal to an enum or const of their location are skipped, as are leaves the schema
//     annotates x-vde-grounding: derived or skip. See Rules and RulesFromSchema.
//   - When no visited leaf is annotated critical, every checked leaf is critical.
//
// # What grounding can and cannot catch
//
// It catches values that are not in the document at all: invented dates, names, venues,
// organisers, amounts and references. It cannot catch a printed value in the wrong field
// (a swapped origin and destination, a departure date stored as the arrival date), a
// wrong choice among printed values, an omission or a semantic misreading: such
// candidates pass. Those remain the judge's job. It flags legitimate derivations (a city
// from an airport code, a currency from a symbol) unless the schema marks them derived or
// skip, and it cannot run without text (Check with a nil *Text is Ungroundable).
package ground

// RulesVersion names the normalization, matching and date-table rules. Bump it whenever
// a rule or table entry changes; it is hashed into service_version.
const RulesVersion = "ground-1"
