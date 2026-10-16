# 032-dining

**Provider.** TheFork (formerly LaFourchette), restaurant reservation confirmation in French.

**Format.** Email with HTML and text parts. A label/value table (`Date`, `Heure`, `Nombre de personnes`, `Au nom de`, `Référence de réservation`) with a highlighted promotion box, address and restaurant phone side by side, then modification terms, offer conditions and a loyalty paragraph.

**Why.** French long date (`samedi 22 mai 2027`) and 24-hour time, `2 personnes` to be read as the party size, a `-30 %` promotion that is a discount and not a price (price stays null and the offer goes in `notes`), and two blocks of conditions where only the modification and cancellation paragraph is the cancellation text, not the offer conditions.
