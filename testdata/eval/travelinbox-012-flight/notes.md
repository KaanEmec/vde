# 012-flight

**Provider.** SAS, direct booking confirmation in Swedish.

**Format.** Email with HTML and text parts, both base64-encoded instead of quoted-printable. Booking reference in the header bar, one flight card with departure and arrival columns, a traveller table with seat and ticket number, baggage, price and fare conditions sections, check-in advice and a car-rental upsell.

**Why.** Checks that the extractor decodes base64 body parts. Swedish dates (`tisdag 9 november 2027`), the Swedish "Bokningsreferens" label, and Swedish number format with a space as thousands separator and a decimal comma (`1 190,00 SEK`). The cabin is printed as the SAS product name (`SAS Go`) with the fare brand (`Light`) beside it. A ticket number and a EuroBonus number are printed near the reference. Departure and arrival are in the same time zone.
