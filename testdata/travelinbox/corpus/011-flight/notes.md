# 011-flight

**Provider.** Wizz Air, direct booking confirmation in English.

**Format.** Email with HTML and text parts, no attachment. Low-cost carrier layout: the "flight confirmation code" in a dashed box, one flight card with times and airports, a passenger table with seats and the WIZZ Priority bundle, a payment summary, check-in and UK entry advice, upsell blocks for hotels and the Discount Club.

**Why.** A short-haul low-cost booking for two adults across a time-zone boundary (Budapest is one hour ahead of London, so the printed 2h 40m flight lands 1h 40m later on the clock). The reference is labelled "flight confirmation code" rather than booking reference or PNR. The price is a sum of fare, add-on bundle, seats and an administration fee with per-person multipliers; only the total paid is wanted. No cabin class is printed, so `cabin` stays null, and the arrival airport has a name (London Luton) while the departure is only a city and code.
