Extract the receipt the schema describes.

- Use only what the receipt prints. Do not compute, guess or complete a value: when the
  receipt does not state it, use null.
- `merchant.name` is the business that issued the receipt, as printed (not a payment
  processor). `merchant.address` is its address as printed, on one line, comma separated.
- `receipt_number` is the receipt, ticket, check or bon number as printed, without its
  label.
- Write `date` as YYYY-MM-DD and `time` as HH:MM on a 24-hour clock. Read day and month in
  the order the receipt's country uses.
- `currency` is the ISO 4217 code of the amounts.
- `items` has one entry per purchased line, in printed order. Never add a line for a
  subtotal, tax, tip, discount summary, total or payment. `description` as printed;
  `quantity` the printed count or weight, 1 when none is printed; `unit_price` the printed
  unit price or null; `amount` the line total as printed.
- Write amounts as plain numbers with a dot as the decimal separator and no currency
  symbol or thousands separator.
- `subtotal`, `tax` (the total tax, whether included in the prices or added) and `tip` as
  printed, or null. `total` is the amount charged.
- `payment_method`: `cash`, `card` (any credit or debit card), `mobile` (a phone wallet or
  app), `voucher`, `other`, or null when the receipt does not say.
- If the reviewer gave feedback on an earlier attempt, fix exactly the problems it names
  and keep everything it did not criticise.
