# 048-multi-booking

**Provider.** Expedia (expedia.co.uk), Flight + Hotel package to Barcelona, English.

**Format.** Email with HTML and text parts, no attachment. One message with a Flights section (outbound and return on British Airways), a Hotel section, and a single package price summary.

**Why.** One email yields three reservations: two flights and a stay. Each carries the reference that is used at check-in, the British Airways confirmation code for both flights and the hotel confirmation number for the stay, while the Expedia itinerary number (in the subject and header) goes in notes. Only a package total is printed, split per traveller rather than per component, so every reservation's `price` is null and the total goes in notes. Flight numbers are printed as `British Airways 478` and become `BA 478`; the eastbound and westbound times cross a one-hour zone difference. The hotel names only the lead guest for 2 adults, while both travellers are on the flights. A tourist tax per person per night is a partial amount and is not a price.
