# 046-cancellation

**Provider.** Booking.com, cancellation confirmation for a hotel in Hamburg, German.

**Format.** Email with HTML and text parts, no attachment. Card layout: a cancellation header with booking number, PIN and cancellation number, the property card with check-in and check-out, price details, and the cancellation policy, followed by an upsell block for other Hamburg properties.

**Why.** A cancellation still has to yield the full stay (property, dates, room, guests) with status `cancelled`. The guest cancelled after the free period, so two prices are printed: the struck-through original total (€ 318) and the cancellation fee (€ 159). The fee is what is charged and is the price; the original total and the cancellation number go in notes. The PIN and the cancellation number sit next to the Buchungsnummer and must not be taken for it. German short dates (`Fr., 22. Okt. 2027`) and "ab 15:00" / "bis 11:00" give the stay times. The cancellation policy text is copied as written even though its deadline has passed.
