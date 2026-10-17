# Job: Normalization, numbers and dates

Version: v0.4 · Epic: 02 Grounding check · Status: done · Depends on: v0.3

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

- [x] `internal/ground/normalize.go`: `Normalize(s string) string`. NFKD, then drop
      combining marks; casefold with Turkish dotted and dotless i mapped to `i`; collapse
      whitespace to one space; strip punctuation at token edges (keep it inside tokens such
      as `LX42QZ`, `A-12`, `10:45`). Precompute an index (`Text`) holding the normalized
      text and its token set once per document.
- [x] `internal/ground/numbers.go`: parse a candidate number and the numbers in the
      document into canonical decimals (`math/big.Rat` or a string form without a trailing
      `.0`). Accept thousands separators `.`, `,`, space and thin space, and both decimal
      conventions. An ambiguous form such as `1.234` matches either reading. Leading
      currency symbols and codes are ignored.
- [x] `internal/ground/dates.go`: for an ISO candidate `YYYY-MM-DD[THH:MM[:SS]]`, generate
      printed forms: `dd.mm.yyyy`, `d.m.yyyy`, `dd/mm/yyyy`, `dd-mm-yyyy`, `yyyy/mm/dd`,
      `dd.mm.yy`, and day-month-name-year in both orders, with full and three-letter month
      names in English, Turkish and German (`21 Mar 2027`, `March 21, 2027`, `21 Mart 2027`,
      `21. März 2027`). Times match `HH:MM` and `H:MM`, and `h:MM am/pm` in English. A
      datetime is grounded when its date and time are both found. The table and its order
      are exported for the docs.
- [x] `internal/ground/doc.go`: package comment listing the rules and `RulesVersion` (a
      string bumped whenever a rule changes; hashed into `service_version`).

## Acceptance criteria

- Table tests per file: diacritics and Turkish casing; punctuation at edges versus inside;
  `1234.5` found in `1.234,50 EUR` and `1,234.50`; `12` not found in `123`; dates found in
  every listed form and not found when day and month are swapped in an unambiguous date
  (`2027-03-21` against `03/21/2027` is a miss, since the US order is not in the table).
- A fuzz test (`FuzzNormalize`) shows that normalization never panics and is idempotent.
- Benchmark: normalizing a 200 kB text takes under 20 ms on one core.

## Deviations

- `Text` and `NewText(s string) *Text` (listed again in job 03) are built here, since
  `Check` takes a `*Text`. Besides the normalized text, `Text` holds word positions (words
  split on every non-letter, non-digit rune) and the sorted numbers the text prints.
  `NewText` on 200 kB takes about 3 ms, `Normalize` about 1.4 ms.
- Normalization also drops format characters (`Cf`: soft hyphens, zero-width spaces),
  folds `ß` to `ss`, turns control characters into spaces and invalid UTF-8 into U+FFFD
  (the fuzz test found that NFKD is not idempotent on invalid input otherwise). Edge
  stripping removes every rune that is not a letter, digit or mark, symbols included, so
  `€1.234,50` and `19%` print their numbers.
- Strings match on word boundaries (no letter or digit around the match). A string of two
  to six words also matches when all its words occur within its own length plus 16 bytes
  of each other, in any order, so `SMITH/JOHN MR` grounds `John Smith`; this is not
  edit-distance matching.
- Numbers are kept as a sorted `[]float64` and compared with a tolerance of 0.005, not as
  `big.Rat`: half a cent covers rounding (`33.333` against a printed `33.33`). A digit run
  directly after a letter is not a number (`LX42` does not print 42), signs are ignored, a
  run with no valid reading (`21.03.2027`) yields its groups as integers, and a run joined
  by thousands spaces also yields its parts (`2 100`).
- The date table is wider than listed: French, Spanish and Dutch month names, `d/m/yyyy`,
  `dd`-padded days with month names, `21-Mar-2027` and `21 de marzo de 2027`. It is
  exported as `DateForms`, `TimeForms` and `Months` with one example per entry. Times also
  match `hh:MM am/pm` and `a.m.`/`p.m.`; a time-only string (`HH:MM[:SS]`) is matched as a
  time. An impossible ISO date (`2027-02-30`) is matched as a plain string.
- `RulesVersion` is `ground-1`; hashing it into `service_version` belongs to the loop.

## Out of scope

Relative dates ("tomorrow"), weekday checks, US month-first order, currencies converted
between units, fuzzy string matching (edit distance).
