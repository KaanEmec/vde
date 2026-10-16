# 004-flight

**Provider.** easyJet, direct booking confirmation in UK English.

**Format.** Email with HTML and text parts, no attachment. One card per direction with the passengers, seats and bags listed under each flight, then a single payment breakdown for the whole booking.

**Why.** A round trip that must become two reservations sharing the booking reference. Two adults and an infant on lap: the infant is a named traveller of type infant with no seat, so each direction has two seats for three travellers. The payment breakdown mixes per-direction fares with infant fees, seats and bags; only the booking total is a price, and it is recorded once, on the outbound reservation, with a note that it covers both flights. Flight numbers are printed in easyJet's ICAO style (`EZY8401`) rather than the IATA code U2. Gatwick is printed with its name and terminal, Geneva only as city and code. The seven-character reference does not follow the six-character airline pattern.
