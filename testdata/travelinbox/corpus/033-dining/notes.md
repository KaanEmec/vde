# 033-dining

**Provider.** Resy, restaurant reservation confirmation in US English.

**Format.** Email with HTML and text parts. A dark card with uppercase labels (`DATE`, `TIME`, `PARTY`, `TABLE`) over the values, followed by short uppercase sections for the venue, house notes, cancellation policy and the card on file.

**Why.** The date is printed without a year (`Saturday, July 17`), so the year has to come from the email's `Date:` header and the weekday. There is no confirmation number at all, so `booking_ref` is null. A per-person no-show fee is cancellation text, not a price, and the card held on file is not a payment. The house note that tables are reserved for 2 hours must not become an end time.
