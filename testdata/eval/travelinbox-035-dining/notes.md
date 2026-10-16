# 035-dining

**Provider.** TableCheck, sending on behalf of a Tokyo sushi restaurant, in Japanese with short English glosses.

**Format.** Email with HTML and text parts, both base64-encoded. A two-column table with Japanese labels and small English labels beneath (`予約番号 / Reservation No.`, `日時 / Date & Time`, `人数 / Party Size`), a prepayment block, venue details, a boxed cancellation policy and a bulleted list of house notes.

**Why.** Japanese date with a weekday in brackets (`2027年11月6日(土) 18:00`), yen without decimals (`¥57,200`, amount `57200`), and the guest name in Latin script in Japanese order (`NAKAMURA MEI 様`) to be turned into `Mei Nakamura`; the English line `Dear Ms. NAKAMURA` confirms which part is the surname. The venue name is printed in Japanese with an English gloss; `name` keeps the Japanese name as printed and the title uses the English one. The stated dining duration of about 2 hours 30 minutes is not an end time, and the cancellation policy is printed only in Japanese.
