# 044-rail

**Provider.** Eurostar, direct booking on eurostar.com (UK site), English.

**Format.** Email with HTML and text parts, no attachment. Yellow hero band, a boxed booking reference, one journey card, a passenger list with coach and seat per person, and a payment summary in pounds.

**Why.** The only cross-border train in the set: departure and arrival are each in their own local time and one hour apart in zone, so 09:01 to 12:20 is a 2h 19m journey, as the email states. An extractor that converts or "corrects" the arrival time gets it wrong. The train is printed as the bare number `9014`, kept as printed. Titles (Mr, Ms) must be dropped from the names, and the price is in GBP. Check-in closing and arrival advice are generic text, not times to extract.
