# 009-flight

**Provider.** British Airways, direct booking confirmation in UK English.

**Format.** Email with HTML and text parts, no attachment. The itinerary is a two-column label/value table with an outbound and an inbound block, followed by passenger, payment, fare-condition and pre-flight sections.

**Why.** A domestic round trip that must become two reservations sharing the booking reference. Flight numbers are printed without a space (`BA1436`) and must be normalised. The cabin is the BA brand name "Euro Traveller", kept as printed. Seats are not assigned (free selection at check-in), so `seat` is null. The terminal appears on both the departure (outbound) and the arrival (inbound) at Heathrow, but only the outbound departs from a printed terminal, so the inbound `terminal` is null. One total covers both flights and is recorded on the outbound reservation; the tax sub-lines (Air Passenger Duty, service charge, carrier charges) must not be taken as the price. The Executive Club number (`BA 41827736`) resembles a flight number.
