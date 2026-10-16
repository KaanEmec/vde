# 014-flight

**Provider.** JAL (Japan Airlines), domestic booking confirmation in Japanese.

**Format.** Email with HTML and text parts, no attachment. Japanese transactional layout: bracketed subject prefix 【JAL国内線】, the passenger addressed as `NAKAMURA HARUTO 様` in Latin capitals, surname first, a reference table, a flight block with 発/着 times, passenger, payment and cancellation sections, airport procedure notes and a package upsell.

**Why.** Japanese date format with the weekday in brackets (`2027年7月3日(土)`), yen amounts with the 円 suffix and no decimals, and the name order reversed relative to "Given Surname". Airports are printed only as `東京(羽田)` and `札幌(新千歳)` without IATA codes, as JAL does on domestic confirmations, so `iata`, `from_iata` and `to_iata` must stay null rather than be inferred. The cabin is printed as 普通席 and is kept as printed. The flight number `JL507` has no space and must be normalised to `JL 507`. The cancellation terms are a fee schedule by days before departure.
