# 039-transfer

**Provider.** Welcome Pickups, airport pick-up in Athens, driver details email in English.

**Format.** Email with HTML and text parts. A coloured driver card (name, phone, languages, rating) above a label/value table of the transfer, a meeting-point box, the cancellation policy and an upsell for further rides.

**Why.** An arrival transfer where the pick-up time equals the scheduled landing of a flight that is printed in the same table: the flight number, its origin (London Heathrow) and the landing time must not turn into a flight reservation or a start place. The driver's name and phone form `driver_contact`, the driver's rating and languages do not. The lead passenger is named while the second adult is only counted, there is no drop-off time, and a single direction means `ends_on` is null.
