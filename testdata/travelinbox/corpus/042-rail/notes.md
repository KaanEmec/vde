# 042-rail

**Provider.** SNCF Connect, booking confirmation for a TGV INOUI journey, French.

**Format.** Email with HTML and text parts, no attachment; the e-ticket lives in the app. The journey is drawn as a vertical timeline with the train in a box between the two stations.

**Why.** French time notation (`10h00`, `11h56`, duration `1h56`) has to become ISO times. The train is printed as `TGV INOUI · Train n° 6611` and must be joined into one value. The platform line says it is shown 20 minutes before departure, so `platform` stays null although the word "Voie" appears. "Salle haute · Fenêtre" describes the seat but is not part of the coach or seat number. The surname also appears alone in capitals as "Nom associé", which is not a second traveller. The provider is SNCF Connect, the app the booking was made with.
