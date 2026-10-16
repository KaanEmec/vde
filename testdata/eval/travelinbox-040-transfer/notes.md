# 040-transfer

**Provider.** Blacklane, chauffeur details email in English sent the evening before a hotel-to-airport ride in Vienna.

**Format.** Email with HTML and text parts. A boxed chauffeur card (name, mobile, licence plate) above a two-column ride summary, then waiting-time and cancellation paragraphs with run-in bold labels.

**Why.** A departure transfer where the destination is the airport, so `iata` belongs to the end place. The chauffeur's name and mobile form `driver_contact` while the licence plate does not; the service class and model are one printed value. An estimated duration of 25 minutes is printed but no drop-off time, so `ends_at_local` stays null. A departing flight in the notes for the chauffeur must not become a flight reservation, and the email was sent the day before the ride rather than at booking time.
