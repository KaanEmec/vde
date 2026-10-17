# Job: Normalization, numbers and dates

Version: v0.4 · Epic: 02 Grounding check · Status: planned · Depends on: v0.3

## Goal

`internal/ground` can decide whether one candidate value occurs in a document's text,
robustly against case, diacritics, whitespace, punctuation, number formatting and the
common printed forms of dates and times.

## Context

The check must be strict enough to flag invented values and loose enough not to flag a
value that is printed differently: `1.234,50` for `1234.5`, `21 Mar 2027` or `21.03.2027`
for `2027-03-21`, `İSTANBUL` for `Istanbul`. `golang.org/x/text` is already a dependency
(unicode normalization for diacritics). The date table is kept small and documented, since
every entry widens what counts as grounded.

## Tasks

- [ ] `internal/ground/normalize.go`: `Normalize(s string) string`. NFKD, then drop
      combining marks; casefold with Turkish dotted and dotless i mapped to `i`; collapse
      whitespace to one space; strip punctuation at token edges (keep it inside tokens such
      as `LX42QZ`, `A-12`, `10:45`). Precompute an index (`Text`) holding the normalized
      text and its token set once per document.
- [ ] `internal/ground/numbers.go`: parse a candidate number and the numbers in the
      document into canonical decimals (`math/big.Rat` or a string form without a trailing
      `.0`). Accept thousands separators `.`, `,`, space and thin space, and both decimal
      conventions. An ambiguous form such as `1.234` matches either reading. Leading
      currency symbols and codes are ignored.
- [ ] `internal/ground/dates.go`: for an ISO candidate `YYYY-MM-DD[THH:MM[:SS]]`, generate
      printed forms: `dd.mm.yyyy`, `d.m.yyyy`, `dd/mm/yyyy`, `dd-mm-yyyy`, `yyyy/mm/dd`,
      `dd.mm.yy`, and day-month-name-year in both orders, with full and three-letter month
      names in English, Turkish and German (`21 Mar 2027`, `March 21, 2027`, `21 Mart 2027`,
      `21. März 2027`). Times match `HH:MM` and `H:MM`, and `h:MM am/pm` in English. A
      datetime is grounded when its date and time are both found. The table and its order
      are exported for the docs.
- [ ] `internal/ground/doc.go`: package comment listing the rules and `RulesVersion` (a
      string bumped whenever a rule changes; hashed into `service_version`).

## Acceptance criteria

- Table tests per file: diacritics and Turkish casing; punctuation at edges versus inside;
  `1234.5` found in `1.234,50 EUR` and `1,234.50`; `12` not found in `123`; dates found in
  every listed form and not found when day and month are swapped in an unambiguous date
  (`2027-03-21` against `03/21/2027` is a miss, since the US order is not in the table).
- A fuzz test (`FuzzNormalize`) shows that normalization never panics and is idempotent.
- Benchmark: normalizing a 200 kB text takes under 20 ms on one core.

## Out of scope

Relative dates ("tomorrow"), weekday checks, US month-first order, currencies converted
between units, fuzzy string matching (edit distance).
