# 031-dining

**Provider.** OpenTable, restaurant reservation confirmation in US English.

**Format.** Email with HTML and text parts. One card holds party size, date and time as three cells, with the confirmation number, diner and seating area on a line below; restaurant details, a note from the restaurant, the cancellation policy and a points block follow.

**Why.** The baseline dining reservation. US date and 12-hour time formats (`Fri, Mar 12, 2027`, `7:30 PM`), a party of four where only the booker is named, no price, a short numeric confirmation number to keep verbatim, and a restaurant note (15-minute table hold, raw bar closing at 10:00 PM) that must not be read as an end time or as the cancellation terms.
