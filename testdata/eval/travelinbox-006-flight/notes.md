# 006-flight

**Provider.** Pegasus Airlines, direct booking confirmation in Turkish.

**Format.** Email with HTML and text parts, no attachment. Yellow and dark grey layout with the PNR box first, then flight, passenger, payment and change/refund sections, an online check-in box and a loyalty promo.

**Why.** A domestic flight for two adults in Turkish. The reference is labelled "Rezervasyon Kodu (PNR)"; e-ticket numbers sit in the passenger table. The date is written out in Turkish ("15 Ağustos 2027, Pazar") while the booking date uses `30.06.2027`; prices use Turkish separators and "TL" (`2.972,98 TL`), which is ISO TRY. Surnames are printed in capitals and must become "Yilmaz". "Temel Paket" is a fare bundle, not a cabin, so `cabin` stays null. Airport names carry the Turkish word "Havalimanı" and the city must be normalised to English (İstanbul to Istanbul). The provider is "Pegasus" as the brand is printed; the sender name is the Turkish "Pegasus Hava Yolları".
