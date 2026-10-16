# 010-flight

**Provider.** Emirates, e-ticket receipt issued on emirates.com, in English.

**Format.** Standalone two-page PDF in the airline "Electronic Ticket Itinerary / Receipt" layout: passenger and ticket block, itinerary table with one row per segment, receipt with a tax breakdown by IATA tax code, fare calculation line, fare conditions, and a second page of check-in advice and legal notices.

**Why.** A connection through Dubai that must become one reservation with two legs, the second leg departing after midnight on the next day. Flight numbers are printed with a leading zero (`EK 092`) and must be normalised to `EK 92`. The e-ticket number, Skywards number, IATA number and approval code sit next to the booking reference and must not be taken for it. Only the total is wanted from a fare plus seven tax lines, and the fare calculation line contains NUC amounts that are not prices. No seats are printed, so `seat` stays null.
