# 001-flight

**Provider.** Lufthansa, direct booking confirmation in German.

**Format.** Email with HTML and text parts and a PDF itinerary and invoice attached (`Reiseplan_VT6Y7Y.pdf`). The PDF repeats the flight in airline style (`14JUN27`, `BRENNER/JANA MS`, `ADT`).

**Why.** The baseline long-haul one-way flight for two adults. German date and number formats (`1.284,52 EUR`), the price split across fare, taxes and surcharge with only the total to be taken, seats per traveller to be joined, ticket numbers that must not be mistaken for the booking code, and fare rules in two labelled sentences that form the cancellation text. Only city and code are printed for the airports, so `name` stays null.
