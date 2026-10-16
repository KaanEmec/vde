# 043-rail

**Provider.** Trenitalia, direct purchase of a Frecciarossa ticket, Italian.

**Format.** Email with HTML and text parts, no attachment; the email itself is the ticket. A red header band, a journey row and a passenger table with one row per traveller carrying service level, offer, coach, seat and price.

**Why.** Seats are spread over the passenger rows and must be joined (`7A, 7B`) while the shared coach is taken once. The destination is printed in Trenitalia's short form `Firenze S. M. Novella`, which stays as the station name while the city becomes Florence. The total must be taken from "Importo totale", not from the per-passenger prices. A transaction code sits next to the PNR and is not the booking reference. The platform is announced only at the station, so it stays null.
