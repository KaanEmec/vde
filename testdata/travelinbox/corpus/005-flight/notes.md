# 005-flight

**Provider.** Air France, direct booking confirmation in French.

**Format.** Email with HTML and text parts and a PDF attached (`Recu_billet_electronique_7VT9MR.pdf`), the "Reçu de billet électronique" with bilingual French and English labels. The PDF repeats the flight in airline style (`AF0344`, `08JUL`, `DUMONT/QUENTIN MR`, `ADT`, booking class `L`) with tax codes and a fare calculation line.

**Why.** A long-haul westbound flight where the arrival is only 1h45 after departure on the clock while the printed flight time is 7h45, so the end time must be read, not computed. French labels ("Référence de réservation", "Numéro de billet", "Siège"), French number format (`689,43 €`) in the email and dot decimals in the PDF, and the ticket number and fare basis next to the reference. The flight number appears as `AF 344` and as `AF0344` with a leading zero. The cancellation text is the two French fare-condition sentences kept in French. City names need English normalisation (Montréal to Montreal) while the airport name keeps its printed form.
