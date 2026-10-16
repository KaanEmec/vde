# 047-cancellation

**Provider.** Vueling, airline-initiated flight cancellation notice, Spanish.

**Format.** Email with HTML and text parts, no attachment. Yellow header, a red-bordered card with the cancelled flight and struck-through times, the passenger names, and two options (free change or refund) with buttons.

**Why.** The original flight must still be extracted in full, with status `cancelled`, and no new flight may be invented: the email offers rebooking but confirms none. The flight number is printed without a space (`VY6104`) and is normalised to `VY 6104`. Times are struck through but are still the scheduled times. Only city and code are printed for the airports, so `name` stays null; Roma becomes Rome. No price is printed, and the passengers have no type. The rebooking and refund options are the change terms and go into `cancellation`; the deadline to choose goes in notes. The EU 261 paragraph is general information, not terms of this booking.
