# Extraction evaluation corpus

The documents extraction quality is measured against. Each folder is one document as a user would forward or upload it, plus the JSON a perfect extractor returns for it.

```
corpus/<nnn>-<kind>/
  source.eml | source.pdf | source.jpg   the document
  truth.json                             expected extraction, valid against shared/extraction-schema.json
  notes.md                               provider, format, and why the document is in the corpus
```

`go test ./testdata ./testdata/redact` (from `backend/`) checks the corpus. The go tool skips directories named `testdata` when it expands `./...`, so `go test ./testdata/...` and `make test` do not reach these tests; name the packages.

## Provenance

Every document is synthetic. There were no real documents to collect, so each one is written to look the way the named provider formats its messages: the subject lines, labels, table layouts, date formats, languages and attachments of that airline, hotel chain, OTA, rail operator, restaurant service or rental company. Names, references, addresses, phone numbers, card digits and dates (all in 2027) are invented. Hotels and restaurants are fictional properties; streets are plausible but house numbers and postcodes are not checked. Phone numbers use ranges reserved for fiction where a country has one (UK 07700 900xxx and 020 7946 0xxx, US 555-01xx), and traveller email addresses use `example.com`.

The sources are rendered from `../tools/docs/<nnn>-<kind>/` by `../tools/build.mjs`:

- `.eml` files are real MIME: `multipart/alternative` with a text and an HTML part (quoted-printable, a few base64), wrapped in `multipart/mixed` when a PDF is attached. The text part is derived from the HTML, as most providers do.
- PDFs, the photo and attached PDFs are rendered from HTML with Playwright 1.49.1 (Chromium). The photo is a screenshot of a printed ticket laid on a table with perspective, shading and JPEG compression.
- Barcodes (the Aztec code on the boarding pass, QR codes on tickets) are drawn with bwip-js 4.11.4.

```
cd backend/testdata/tools
npm ci                                   # playwright and bwip-js, pinned in package-lock.json
npx playwright install chromium          # browser build for Playwright 1.49.1, kept outside the repo
node build.mjs                           # or: node build.mjs 007 012
```

Rendering depends on the fonts installed on the machine, so rebuilt PDFs and the photo are not byte-identical across machines. That does not matter for evaluation; commit what you rendered.

## Redaction

`CLAUDE.md` requires fixtures under `backend/testdata/` to be redacted. `testdata/redact` does it and is run over every file before commit, including this synthetic corpus, so the rule holds unchanged when real documents arrive:

```
cd backend
go run ./testdata/redact testdata/corpus testdata/tools/docs     # rewrite in place
go run ./testdata/redact -check testdata/corpus testdata/tools/docs   # what TestRedacted runs
```

It replaces, with stable fakes:

| What | Found by | Replaced with |
| --- | --- | --- |
| Names | traveller names in any `truth.json`, `-name` flags, salutations ("Dear", "Hallo", "Bonjour"), person labels ("Passenger:", "Guest name", "Gast:") and the airline form (surname, slash, given name, title, in capitals) | a name part from the fake lists in `redact.go`, same case, everywhere the part occurs as a whole word, including ASCII spellings (Müller, Mueller, Muller) |
| References | `booking_ref` in `truth.json`; values after reference labels (booking reference, PNR, PIN, ticket number, Buchungsnummer, localizador, 予約番号, ...) | a value of the same shape derived from a hash: digits stay digits, letters stay letters in the same case, separators stay |
| Card numbers | Luhn-valid 13 to 19 digit numbers; masked numbers (four masked groups or "ending in" followed by four digits) | `4242...` of the same length with a valid check digit; masked digits become `4242` |
| Passport and ID numbers | values after passport, ID card, DNI, kimlik labels | `X23X56X89` style placeholder of the same shape |
| Email addresses | any address except role accounts (`noreply@`, `reservations@`, ...) and reserved domains (`example.com`, `.test`) | `user.<hash>@example.com` |

Dates, times, places, prices and layout are kept. Replacements are a pure function of the original value, so a name or reference gets the same fake in the source, `truth.json` and `notes.md`. Fakes are recognised on later runs (fake-list names, `example.com`, `4242` numbers, and references recorded in `testdata/redact/known-fakes.txt`), so a second run changes nothing; `TestRedacted` relies on that.

Limits, which is why a person still reads the diff:

- PDFs, images and binary attachments cannot be rewritten. The tool lists them as "not inspected". For this corpus the HTML they are rendered from is redacted before rendering; a real PDF or photo has to be checked by eye or re-created from redacted text.
- A name is only found when `truth.json`, a flag, a salutation or a person label points at it. A real name made only of parts on the fake lists is kept. Names in non-Latin scripts (kanji, Greek, Cyrillic) are not found; the Japanese, Chinese and Greek documents here print names in Latin script.
- A name part that is also an ordinary word (Rose, Park) is replaced wherever that word occurs. Use `-v` to see the table and fix such cases by hand.
- Phone numbers, street addresses and loyalty numbers without a label are not touched.

## How to add a document

1. Pick the next number and a kind from the table below (or add a kind and a row).
2. Real document: put it in `corpus/<nnn>-<kind>/source.*`. Synthetic document: write `tools/docs/<nnn>-<kind>/doc.json` and its HTML (see the header of `tools/build.mjs`), using names from the fake lists in `redact.go`.
3. Write `truth.json` and `notes.md` by hand, following the conventions below.
4. `go run ./testdata/redact testdata/corpus testdata/tools/docs`, then render with `node build.mjs <nnn>` if the source is synthetic. Read the diff.
5. `go test ./testdata ./testdata/redact` and update the counts below.

`notes.md` has three paragraphs: `**Provider.**` who sent it and in which language, `**Format.**` the file and its parts, `**Why.**` what makes it hard or worth measuring.

## Labelling conventions for truth.json

`truth.json` is exactly what a perfect extractor returns: every field is written out, `null` when the document does not say, and `confidence` is `1.0` for every field that is not null and absent for fields that are null. Values come from the document; the only normalisation is listed here. `title` and `notes` are free text and are compared loosely; everything else is compared exactly.

| Field | Convention |
| --- | --- |
| reservations | One per bookable item: each flight journey, each stay, each table, each rental. A round trip is two flight reservations sharing a `booking_ref`. A connection is one reservation with `details.legs`. |
| title | English. Flights and trains "Frankfurt to New York", "Hamburg to Nairobi via Amsterdam"; stays the property name; dining "Dinner at X" or "Lunch at X"; rentals "Car rental in Munich"; transfers "Transfer from X to Y". |
| provider | The company the booking was made with: the airline or hotel chain when booked direct, the OTA (Expedia, Booking.com, eDreams) when booked through one. |
| booking_ref | Verbatim. When an OTA prints both its own reference and the operator's (airline locator, hotel confirmation number), the operator's reference is used, because that is what the traveller shows at check-in; the other one goes in `notes`. |
| status | `confirmed` for confirmations, tickets and boarding passes; `cancelled` for cancellations; `pending` when the document says the booking awaits confirmation. |
| starts_at_local, ends_at_local | Local wall-clock time of the place, `YYYY-MM-DDTHH:MM:SS`. A bare date when no time is printed. Stays: check-in date with the earliest check-in time, check-out date with the latest check-out time. Dining: `ends_at_local` only if an end time is printed. |
| places | `name` as printed without codes (airport such as "Amsterdam Schiphol", station, hotel, restaurant, rental branch), null when only a city and a code are printed. `address` as printed on one line, comma separated. `iata` only when printed. `city` in English. `country` ISO 3166-1 alpha-2 derived from the city. Stays and dining have `end_place: null`; rentals and transfers repeat the place when pick-up and return are the same. |
| travellers | Every person named as travelling, "Given Surname", natural capitalisation, no titles. `type` only when the document states it for that person (Adult, Erwachsener, Child, Infant), otherwise null; a party count such as "2 adults" does not type a named person. Null when no traveller is named. |
| price | The total charged, as printed, including taxes; `amount` with a dot and no thousands separator. Null when only a per-night or partial amount is printed. When a deposit is paid and the rest is due at the property, the full total. A price covering several reservations (a round trip, a flight and hotel package) is not split: each reservation's price is null unless printed separately, and the package total goes in `notes`. |
| cancellation | The cancellation or change terms as written, in the document's language; several sentences joined with a space. |
| flight details | `number` is the airline code, a space and the number without leading zeros ("LH 400"). `carrier` is the marketing carrier as printed; "operated by" goes in `notes`. Top-level fields describe the journey: first flight number and carrier, overall origin and destination, departure terminal and seats of the first leg, cabin as printed. `seat` lists every traveller's seat, comma separated. `legs` only when there is more than one segment, else null. `gate` only when printed. |
| lodging details | `room` as printed, `guests` the number of guests, `address` and `phone` as printed, `checkin_time` and `checkout_time` as `HH:MM`. |
| rail details | `train` as printed ("ICE 1003", "Frecciarossa 9531"), `coach` and `seat` as printed (several seats comma separated), `platform` only when printed. |
| trip_hint | `destination` is the city the traveller goes to (arrival city of a journey, city of a stay, table or rental) in English, with its country. `starts_on` is the date of the earliest start, `ends_on` the date of the latest end (or start, when there is no end). A document holding only one-way journeys (flights, trains, transfers in one direction) has `ends_on: null`, because it does not say when the trip ends. |

## Kind counts

| Kind | Count | Notes |
| --- | --- | --- |
| flight | 15 | 3 multi-leg, 2 round trips, 1 via an OTA, 1 standalone PDF |
| hotel | 15 | chains, OTAs, a short-term rental, a hostel, an independent hotel, 1 standalone PDF |
| dining | 5 | OpenTable, TheFork, Resy, Quandoo, TableCheck |
| car | 3 | 1 one-way, 1 standalone PDF voucher |
| transfer | 2 | airport pick-up and chauffeured drop-off |
| rail | 5 | DB, SNCF, Trenitalia, Eurostar, Amtrak |
| cancellation | 2 | a hotel and a flight |
| multi-booking | 1 | flight and hotel in one email |
| ticket-photo | 1 | phone photo of a printed ferry ticket |
| boarding-pass | 1 | boarding pass printed from online check-in, with an Aztec code |
| total | 50 | |

Languages: English, German, French, Spanish, Italian, Turkish, Swedish, Japanese, Chinese and Greek.
