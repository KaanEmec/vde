# 002-flight

**Provider.** KLM, direct booking confirmation in English.

**Format.** Email with HTML and text parts, no attachment. The two segments are shown as a vertical timeline with a transfer bar between them, followed by passenger, payment and fare-condition tables.

**Why.** A same-day connection that must become one reservation with two legs, not two reservations. The first leg is operated by KLM Cityhopper while marketed as KLM, so the operating carrier belongs in `notes` and `carrier` stays KLM. The destination is two time zones east of the origin, so the end time (20:40) cannot be computed from the travel time without zone data. Airport names are printed below the city and code, the ticket and Flying Blue numbers sit next to the booking code, and seats are explicitly not yet selected, so `seat` stays null.
