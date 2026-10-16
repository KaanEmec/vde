# 008-flight

**Provider.** Delta Air Lines, direct booking receipt in US English.

**Format.** Email with HTML and text parts, no attachment. A "Flight Receipt" with the confirmation number in a band, a flight table with both segments, a passenger table with SkyMiles, eTicket numbers and per-flight seats, then payment, fare rules and baggage fees.

**Why.** A domestic connection across three time zones that must become one reservation with two legs. Times use the 12-hour clock (`7:00am`, `3:25pm`) and must be converted to 24-hour; dates appear as `Thursday, April 22, 2027` and as `Thu, 22APR`. The reference is labelled "Confirmation #", and the eTicket and SkyMiles numbers are other long numbers nearby. Seats are listed per passenger per flight and must be regrouped per leg (`23A, 23B` and `31A, 31B`); the top-level seat repeats the first leg. Names are printed in capitals without a passenger type, so `type` is null. The price uses US separators with a `$` sign and "USD". Bag fees in the baggage section are not part of the total.
