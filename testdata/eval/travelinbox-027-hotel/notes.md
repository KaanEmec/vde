# 027-hotel

**Provider.** Trip.com (携程), booking confirmation for a hotel in Shanghai, in Simplified Chinese.

**Format.** Email with HTML and text parts, both base64 encoded. Blue brand bar, an order number box, the hotel name in Chinese with its English name below, a check-in and check-out band, labelled rows and a per-night price list.

**Why.** Base64 body parts, Chinese dates with weekday (`2027年4月14日 周三`) and times written as `14:00以后` and `12:00以前`, two guests in the `SURNAME/GIVEN` form (`CHEN/MEI，CHEN/WEN`) separated by a full-width comma, and a price list where a member coupon is subtracted from the room total, so the amount paid (¥2,804) differs from the room total (¥2,904); `¥` here means CNY. The title uses the printed English name; the place name is the Chinese name as printed. A masked guest mobile number sits next to the hotel phone.
