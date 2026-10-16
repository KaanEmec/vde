# 049-ticket-photo

**Provider.** Blue Star Ferries, a printed passenger ticket issued at a port agency, bilingual Greek and English.

**Format.** JPEG phone photo (1080 x 1440) of the printed ticket lying on a wooden table: the sheet is rotated and seen in perspective, with a drop shadow, uneven light from the top left and a horizontal fold crease across the middle. Pre-printed bilingual labels with values in a typewriter face, a QR code, and a tear-off issue stub.

**Why.** The only photographed document: text has to be read from an image that is skewed, unevenly lit and creased. Every label is in Greek with English beneath, and the ports are printed in both scripts (`ΠΕΙΡΑΙΑΣ / PIRAEUS`); names are printed surname first in capitals (`FERREIRA SOFIA`). Only the departure time is printed, so `ends_at_local` stays null, and the ports are cities without terminal names. The booking code sits next to a ticket number, and the total `€ 83,00` must be taken rather than the per-person fare and port dues. Ferry details use the empty schema, so vessel, class and seats go to `notes`. No cancellation terms are printed.

**Barcode.** Barcode (qr): `BSF;PT1X4Q;247856781;PIR;NAX;17082027;0725;2`
