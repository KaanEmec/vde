# 018-hotel

**Provider.** Expedia, hotel booked through the OTA, US English.

**Format.** Email with HTML and text parts. Expedia layout: rounded white card on a grey background, itinerary number in the subject, the header and a two-column reference box, then hotel, check-in and check-out columns, room details, a nightly price summary, a OneKey rewards banner and cancellation terms.

**Why.** Two references sit side by side: the 13-digit Expedia itinerary number, repeated in the subject, header and help text, and the hotel's 8-digit confirmation number printed once. The hotel's number is the `booking_ref` and the itinerary number goes to notes. US formats throughout: 12-hour times ("starts at 3:00 PM", "is 11:00 AM"), month-first dates without the year in the headline, and a phone number in parentheses. Two nightly rates, a subtotal, and taxes and fees lead to the total; the OneKeyCash amount and the $100 per night card hold are distractors. The cancellation deadline appears twice, in property local time and in Eastern Time.
