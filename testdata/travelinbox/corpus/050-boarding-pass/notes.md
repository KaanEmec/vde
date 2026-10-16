# 050-boarding-pass

**Provider.** Turkish Airlines, boarding pass from online check-in, bilingual Turkish and English.

**Format.** Standalone one-page A4 PDF as printed from online check-in: the boarding pass with an Aztec code on a tear-off stub, then a cut line, important information in Turkish and English, and Miles&Smiles and app promotions. The Aztec code carries an IATA BCBP mandatory-items string (`M1YILMAZ/EMRE MR      EUT0V7I ISTLHRTK 1979 255Y023C0087 100`).

**Why.** A boarding pass rather than a confirmation: no price, no fare rules, and the traveller is printed only in airline form (`YILMAZ/EMRE MR`) with no passenger type, so `type` stays null. Every label is bilingual (`Kalkış / Departure`, `Kapı / Gate`). Several times sit side by side (boarding 14:45, departure 15:30, arrival 17:35) and only departure and arrival belong in the timeline; the boarding time and group go to `notes`. The only terminal printed is the arrival terminal at Heathrow, so the departure `terminal` stays null and the arrival terminal goes to `notes`. The PNR, e-ticket number, Miles&Smiles number and sequence number are all printed and only the PNR is the booking reference. The date appears in two forms (`12 SEP 2027`, `12SEP`) and as Julian day 255 inside the barcode.

**Barcode.** Barcode (aztec): `M1YILMAZ/EMRE MR      EUT0V7I ISTLHRTK 1979 255Y023C0087 100`
