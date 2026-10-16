# 015-flight

**Provider.** eDreams (online travel agency), booking confirmation in Spanish for a Vueling flight.

**Format.** Email with HTML and text parts, no attachment. OTA layout: the eDreams booking number in the page header and the subject, a box with the airline localizador and the eDreams number, a vertical timeline for the flight, passenger and baggage line, price summary with the agency service fee, change and cancellation terms, an online check-in note and a Prime upsell.

**Why.** Two references of different shapes: the 10-digit eDreams number is the more prominent one (subject, header) but `booking_ref` must be the Vueling localizador `T0AT6F`, with the eDreams number in `notes`. The provider is the agency while the carrier is Vueling. The total includes an agency service fee. The arrival airport is printed in Spanish (`París Orly`) and kept as printed while the city is normalised to Paris; the cabin is printed as `Económica`. No seat is assigned yet ("asignación gratuita al facturar"), so `seat` stays null.
