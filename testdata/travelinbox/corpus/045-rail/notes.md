# 045-rail

**Provider.** Amtrak, direct booking on amtrak.com, US English.

**Format.** Email with HTML and text parts and a US Letter PDF eTicket attached (`Amtrak_eTicket_0N3O1D.pdf`) with a QR code. The PDF repeats the trip in capitals (`NEW YORK (PENN STATION), NY - WASHINGTON (UNION STATION), DC`, `2157 ACELA`).

**Why.** 12-hour times (`9:00 AM`, `11:52 AM`) and US dates (`Thu, Sep 16, 2027`, purchase date `09/02/2027`) have to be normalised. The station codes `NYP` and `WAS` are Amtrak codes, not airport codes (WAS is also the IATA city code for Washington), so `iata` stays null. The train is printed number first (`2157 Acela`) and is kept that way. The 13-digit ticket number on the PDF must not be taken as the reservation number. Car and seat sit per passenger in an "Accommodation" column and are split into coach `3` and seats `9C, 9D`; the track is posted only at the station.

**Barcode.** Barcode (qr): `AMTRAK|RES:0N3O1D|TKT:2472423829216|TRN:2157|NYP-WAS|20270916|BC|PAX:2|Teodor Novak`
