# Suggestions for the TravelInbox schema and instructions

Status: v1.2 (job 08), for the owner to send to TravelInbox; this repository sends nothing
and changes neither file, except the corpus copies of the instructions, amended on
2026-10-20 as the last table says. Reviewed against the checklist in
[writing-instructions.md](../api/writing-instructions.md#checklist): descriptions, format and
unit, closed enums, the meaning of null, nullable against optional, flatness, and agreement
between schema and instructions.

The schema is `FromTravelApp/extraction-schema.json` (copy:
[`testdata/travelinbox/corpus/schema.json`](../../testdata/travelinbox/corpus/schema.json)),
the instructions
[`extractor.md`](../../testdata/travelinbox/corpus/extractor.md) and
[`judge.md`](../../testdata/travelinbox/corpus/judge.md). Fields are named by JSON pointer
into the schema file. Nothing here quotes a document. "Proposed" texts are the
description (or change) to add; none changes what a valid result is unless marked
**breaking**, and the extraction service accepts either schema.

What the review found, in short: the schema is closed, uses `$defs` and nullable types well
and constrains formats with patterns, but most descriptions that the instructions rely on
exist only in the instructions, the three-level `details` shape depends on `oneOf`, and the
eleven `allOf` rules about `confidence` give the model many ways to fail validation.

## Schema

| JSON pointer | Problem | Proposed description or type |
| --- | --- | --- |
| `/description` | Says unknown values are "null or omitted". The instructions say null; omitting leaves "not in the document" and "forgotten" indistinguishable. | "What the extraction service returns for one document. A value the document does not give is null. Local times are wall-clock times in the zone of the place they belong to." |
| `/properties/reservations` | No description; the unit of a reservation and the empty case live only in the instructions. | description: "One item per booking line, in document order; several bookings are several items. `[]` when the document contains no reservation." |
| `/$defs/text` | The shared string has no description, so no field that refers to it says what null means. | description: "Text exactly as printed unless the field says otherwise. Null when the document does not give it." (A `$ref` property can add its own description next to the reference.) |
| `/$defs/iata` | Type and pattern only. A model cannot tell airport from carrier codes, or that lower case is wrong. | description: "IATA airport code, three capital letters. Null when the document names no airport and none can be derived." |
| `/$defs/country` | Says "ISO 3166-1 alpha-2" but not that it is upper case or when null. | description: "Country as ISO 3166-1 alpha-2, two capital letters (derived from a named city or address is allowed). Null when not determinable." |
| `/$defs/local_datetime/description` | "RFC 3339 date-time without offset" is a contradiction: RFC 3339 requires an offset. The pattern is right; the name is not. | "Local date and time at the place, `YYYY-MM-DDTHH:MM:SS`, with no offset and no `Z`, as printed (never converted to another zone). A bare `YYYY-MM-DD` when the document gives no time; never `T00:00:00` for unknown. Null when not given." |
| `/$defs/local_time` | "Wall-clock time, HH:MM" does not say 24-hour. | "Wall-clock time at the place, 24-hour `HH:MM`. Null when not given." |
| `/$defs/date` | Fine, no null rule. | add "Null when the document gives no date." |
| `/$defs/count` | No description: guests, party size and the lower bound are unexplained. | "Whole number of people or items, at least 1, as printed. Null when not given." |
| `/$defs/place` | Object with five optional, nullable fields; no description of which text goes where or that `iata` is airports only. | description: "A location. `name` as printed without codes, null when only a city and a code are printed; `address` as printed; `iata` only for airports; `city` the English name; `country` ISO alpha-2. Fields the document does not give are null." Add the same one-liners to `/$defs/place/properties/name`, `address` and `city`. |
| `/$defs/traveller/properties/name` | The name order and normalisation (given then family, titles dropped) are only in the instructions. | description: "Full name, given name first, then family name, without titles. Capitalise all-capital words." |
| `/$defs/traveller/properties/type` | Closed enum with null, good; no rule for null, and nothing says whether a party count types a person. | description: "Only when the document states it for that person (adult, child, infant, an age band, a fare code); a party count such as 2 adults does not type a named person; otherwise null." |
| `/$defs/price/description` | "Total price as printed" does not say for what (this reservation, all travellers) or when the whole object is null. | "Total paid for this reservation, for all travellers, as printed. Null when only a per-night or per-person price is printed, or when one total covers several reservations." |
| `/$defs/price/properties/amount` | Pattern allows three decimals; no statement that thousands separators are not allowed. | description: "Decimal string with a dot and no thousands separator, with the decimals as printed (`1234.50`)." |
| `/$defs/price/properties/currency` | "ISO 4217." only. | "ISO 4217 code, three capital letters; derived from an unambiguous symbol is allowed." The object is `required: [amount, currency]`, so a price with an unknown currency can only be `null`; say so in the object's description. |
| `/$defs/confidence/description` | The scale (0.95 to 1 copied; 0.8 to 0.95 reformatted; below that inferred) and "one number per object field" are only in the instructions. | add: "0.95 to 1 printed and copied; 0.8 to 0.95 reformatted or derived; 0.5 to 0.8 inferred or hard to read. An object field gets one number for the whole object. Null fields get none." |
| `/$defs/reservation/allOf` | Eleven `if`/`then` rules make `confidence` carry a key for every non-null field. A model that fills a field and forgets its confidence fails validation and loses an attempt; the rules also need the field list kept in step in three places (`allOf`, `propertyNames`, the instructions). | **Proposed (breaking for validators):** drop the eleven rules and state the requirement in `/$defs/confidence/description`; whether a missing confidence is acceptable is TravelInbox's call. Alternatively keep one confidence number per field inside the field (see the next row). |
| `/$defs/reservation/properties/confidence` | A parallel object that repeats the reservation's field names as keys. | Flatter option: **breaking**, a nullable `confidence` number next to each scalar field, or drop per-field confidence for fields the consumer does not use. If the parallel object stays, keep `required: [kind, title]` and the `propertyNames` enum only. |
| `/$defs/reservation/properties/kind` | Closed enum, good, but the twelve values are explained only in the instructions, and `other` is also the shape of `bus` and `ferry`. | description with one clause per value, for example "flight; lodging (hotel, apartment, hostel, holiday rental); transfer (pre-booked taxi, shuttle or driver); car_rental; rail; bus; ferry; activity (tour, excursion, anything with a meeting point); dining; event (concert, match, show, entry at a venue); insurance; other (visa, parking, lounge, anything else; use only when no other value fits)". |
| `/$defs/reservation/properties/title` | The only field the model writes rather than copies, with a composition rule only in the instructions. | description: "A short English label composed from the document, not copied: transport as `<from city> to <to city>`, lodging the property name, events and activities their printed name." Mark `x-vde-grounding: skip` (the service ignores the keyword in validation) so the grounding check does not look for it. |
| `/$defs/reservation/properties/provider` | No description; the pairing with `booking_ref` and the name form are only in the instructions, and the corpus labels the platform where the instructions name the operator (see the last table). | "The company the traveller deals with: the operator when an operator reference is printed, else the platform or agency, by its short brand name as printed (the hotel chain when booked direct), without legal form or loyalty programme. Null when not given." |
| `/$defs/reservation/properties/booking_ref` | "Verbatim as printed." with a `$ref`; no pairing, no null rule. | "The reference the traveller quotes to `provider`, character for character as printed. Null when the document prints none." |
| `/$defs/reservation/properties/status` | Closed enum with null, but null is unexplained. | "confirmed: the document confirms the booking or is a ticket or boarding pass; pending: awaiting confirmation, payment or approval; cancelled: it says the booking is cancelled; null otherwise." |
| `/$defs/reservation/properties/starts_at_local`, `ends_at_local` | The rules for which time is the start or end per kind (flights, stays, rentals) are only in the instructions. | Describe each per kind in one sentence, for example "flights: first departure; lodging: check-in date and the start of the check-in window". |
| `/$defs/reservation/properties/start_place`, `end_place` | Direction and the null rule for `end_place` (ends where it starts) are only in the instructions. | "Where the reservation starts: departure airport, station or port, the property, the venue." / "Arrival airport, station or port, drop-off place. Null when it ends where it starts (a stay, a meal, an event)." |
| `/$defs/reservation/properties/travellers` | Nullable array; whether an empty array or null means "none named" is unstated. | "Each person named in the document, in document order. Null (not `[]`) when none is named. A headcount belongs in `details`, not here." |
| `/$defs/reservation/properties/cancellation` | "Cancellation terms as written" with a `$ref`; no length rule. | "The cancellation and change terms as written, trimmed to the policy sentences. Null when none are given." |
| `/$defs/reservation/properties/details` | `true` accepts anything on its own; the only constraint is the `oneOf` below, which a model reads poorly. | Add a description naming the object per kind ("flight: flight; lodging: lodging; ...; bus, ferry, other: an empty object or null") and keep the `oneOf` for validation. |
| `/$defs/reservation/oneOf` | Ten branches keyed on `kind`, with `bus`, `ferry` and `other` sharing an empty object. A `oneOf` is checked after the answer is written, and a wrong `details` for the `kind` costs an attempt. | Keep for validation if the consumer needs it; do not rely on it to steer. Flatter option, **breaking**: one optional `details` object per kind as sibling nullable fields (`flight`, `lodging`, ...) instead of a polymorphic one. |
| `/$defs/reservation/required` | Only `kind`, `title` and `confidence` are required; every other field is optional and nullable at once, so a forgotten field and an absent value look the same. | **Breaking for producers only:** list `provider`, `booking_ref`, `status`, `starts_at_local`, `ends_at_local`, `start_place`, `end_place`, `travellers`, `price`, `cancellation` and `details` in `required`, each already nullable, and state in the root description that null means "not in the document". Consumers that already accept null lose nothing. |
| `/$defs/flight/description` | Journey fields and `legs` overlap (number, carrier, airports, seat, cabin, terminal appear at both levels). | Keep the description and add per field: "`seat` and `gate` are null at the top level when `legs` is used"; `legs` "null for a single flight". Flatter option, **breaking**: always `legs`, no top-level duplicates. |
| `/$defs/flight/properties/terminal`, `/$defs/flight_leg/properties/terminal` | "Always the departure terminal" is only in the instructions. | description: "Departure terminal." |
| `/$defs/flight/properties/seat`, `/$defs/flight_leg/properties/seat` | The multi-traveller format (all seats, in traveller order, separated by `, `) is only in the instructions. | description: "Seat as printed; for several travellers all seats in traveller order, separated by `, `." |
| `/$defs/flight/properties/carrier`, `/$defs/flight_leg/properties/carrier` | The codeshare rule is only in the instructions. | description: "Airline name as printed with the flight number; for a codeshare the airline whose number is shown." |
| `/$defs/lodging/properties/checkin_time`, `checkout_time` | Not stated that these repeat the times in `starts_at_local` and `ends_at_local`, or that a window gives its start. | "Start of the check-in window" / "Check-out time, as printed." |
| `/$defs/other/description` | "Also used for bus and ferry." The object is closed and empty, so the only valid values are `{}` and null. | "No details are defined for this kind: `{}` or null." |
| `/properties/trip_hint/properties/starts_on`, `ends_on` | Only the object has a description; the span rule is in the instructions. | "Earliest date of any reservation in the document." / "Latest start or end date of any reservation in the document; do not extend past what the document shows. Null when the document holds only one-way journeys." |
| `/properties/trip_hint/properties/destination` | The rule for which city counts is only in the instructions. | "English name of the trip's main city: where the outbound transport arrives, else the city of the stay, event or activity. Null when the reservations belong to unrelated trips." |
| `/properties/notes` | Description is a `$ref` sibling; the typical contents are not said. | "Short English sentences: conflicts between values, inferred years, changes, references not used, anything the schema cannot hold. Null when nothing needs saying. Never a passport, ID or card number." |

## Instructions

Not schema, but found while checking the two for agreement; pointers are replaced by the
section heading in the file.

| File and section | Problem | Proposal |
| --- | --- | --- |
| `extractor.md`, opening paragraph, and `judge.md`, opening paragraph | "Return only the JSON object: no prose, no code fences." contradicts the answer blocks the service now asks for (`<analysis>` then `<json>`, or `<verdict>`). The service reads the line as describing the block's content, so it still works. | Delete the line from both; keep nothing else about the answer format in the extractor (the service owns it). The judge's "Output" section can keep the verdict object's shape. |
| `extractor.md`, "Dates and times" (a date without a year) | Refers to "the document's own date (sent or issue date)" without saying where to find it for an email. | Say: "for an email, the year of `sent` in the first line of the document". The service now writes `type: email · sent: <RFC 3339> · pages: N` as the first line (the `sent` value is the Date header; it is left out when the header cannot be parsed). |
| `extractor.md`, "Ground rules" (forwarded email) | "In a forwarded email, the forwarding wrapper ... is not booking data" depends on the model finding the wrapper. | Refer to the labels: "the booking is usually between `[forwarded message]` and `[end of forwarded message]`; the lines outside are the forwarder's". The service never removes text, so a section it does not label is still read as before. |
| `extractor.md`, "Confidence" and `/$defs/confidence/description` | The scale is stated in both; they agree today. | Keep one source: the schema description (proposed above), and in the instructions only "fill `confidence` as the schema describes". |
| `judge.md`, "What the extractor was told" | Restates the extractor's rules by hand; any later change to the extractor file must be repeated. | Keep, and treat the two files as one change when editing; the checklist asks for the same words in both. |

## Instructions against the corpus labels (2026-10-20)

The corpus labelling conventions (`testdata/travelinbox/corpus/README.md`, the rules
`truth.json` follows) and the instructions disagreed on five fields, which caused most of
the recurring mismatches ([quality.md](../quality.md#why-the-same-fields-keep-failing-2026-10-20)).
The corpus copies of `extractor.md` and `judge.md` now state the first four; TravelInbox's
own files need the same sentences.

| Field | Corpus label | Instructions before | Now in the corpus copies |
| --- | --- | --- | --- |
| `trip_hint/ends_on` | null for a document holding only one-way journeys | the latest date of any reservation | the corpus label |
| place `name` | as printed without codes; null when only a city and a code are printed | as printed; the airport's name may be derived from its code | the corpus label; the derivation is removed |
| traveller `type` | a party count does not type a named person | "adult" among the stated types | the corpus label |
| `provider` name form | the short brand (the chain when booked direct) | "copy as printed" | the corpus label, without legal form, loyalty programme or web address |
| `provider` when booked through a platform | the platform, with the operator's reference as `booking_ref` | the operator, paired with its reference | unchanged: TravelInbox decides. Recommended: relabel the seven platform cases to the operator (platform and its reference in `notes`), which keeps the pairing |
