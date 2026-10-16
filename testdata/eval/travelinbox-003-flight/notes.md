# 003-flight

**Provider.** Ryanair, direct booking in English (UK site).

**Format.** Email with HTML and text parts, no attachment. A "Travel Itinerary" with the reservation number and status at the top, then flight, passenger and payment tables in Ryanair's navy and yellow, a check-in reminder box, fare rules and hotel and car-hire upsells.

**Why.** A low-cost carrier layout. Dates use a two-digit year (`Thu, 20 May 27`) and the reference is called "Reservation number". "Regular" is a fare bundle, not a cabin, so `cabin` stays null. The payment summary lists the fare (with taxes printed inside it as "incl. taxes & charges £13.00") and a 20kg bag add-on; only the total paid is the price. The airport is printed as "Milan Bergamo", so the city is Milan even though the airport lies outside it; the upsell block also mentions Bergamo and Milano Centrale. Check-in opening rules and the £55 airport fee are not cancellation terms.
