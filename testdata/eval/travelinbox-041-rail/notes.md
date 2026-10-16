# 041-rail

**Provider.** Deutsche Bahn, direct booking on bahn.de, German.

**Format.** Email with HTML and text parts and the PDF `Online-Ticket` attached (`Ticket_150163649771.pdf`), which carries an Aztec code. Both parts print the DB connection table (Halt, Datum, Zeit, Gleis, Produkte, Reservierung).

**Why.** The baseline long-distance train. The reference is a 12-digit Auftragsnummer, not a letter code. Coach and seats come abbreviated (`Wg. 7, Pl. 61, 63`) and must be split into coach and seats. Platforms are printed for both departure and arrival (`1` and `19`, marked "ohne Gewähr"); the departure platform is the one taken. The ticket covers 2 adults but names only the ticket holder, so one traveller is listed and the count goes in notes. The total includes the seat reservation, and the VAT line must not be taken as a price. The city is normalised to English (München Hbf, Munich).

**Barcode.** Barcode (aztec): `#UT01DB|AUFTRAG:150163649771|ICE 1007|18.03.2027|8011160-8000261|BERLIN HBF-MUENCHEN HBF|KL2|ERW2|SPARPREIS|Elif Yilmaz`
