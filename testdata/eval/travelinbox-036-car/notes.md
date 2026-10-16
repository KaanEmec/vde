# 036-car

**Provider.** Sixt, direct car rental reservation confirmation in German.

**Format.** Email with HTML and text parts. The reservation number sits in an orange box; pick-up and return are two columns with the same branch, followed by a grey box with desk and return instructions, the vehicle table, a price breakdown, a list of what to bring and the cancellation terms.

**Why.** Pick-up and return at the same airport branch, so both places are repeated. German abbreviated weekdays and numeric dates with `Uhr`, a branch name printed in German (`München Flughafen`) while the city is normalised to Munich, a vehicle group code (`CDAR`) next to the vehicle description, a price breakdown with VAT and a 300 EUR deposit that must not be taken as the price, and pick-up and return instructions that belong in `details`, not in the place addresses.
