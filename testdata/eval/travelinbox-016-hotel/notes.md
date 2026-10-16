# 016-hotel

**Provider.** Booking.com, hotel booked through the OTA, English.

**Format.** Email with HTML and text parts. Booking.com layout: checklist header, property card with check-in and check-out columns, a label/value block with confirmation number, PIN code and booker, then room, price, payment and cancellation sections.

**Why.** The dotted Booking.com confirmation number (`9956.401.779`) must be kept verbatim with its dots and not confused with the four-digit PIN printed right below it. Check-in and check-out are given as "from 15:00" and "until 11:00" on lines separate from the dates. The price block adds a percentage city tax to the room total, and the total is paid at the property, so the masked card only guarantees the booking. Two adults stay but only the booker is named.
