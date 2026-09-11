# Reference properties

- Preserve `BILL-REQ-42`, `billing`, `external-api`, and the interface trace.
- Select the complex pattern because the valid-input precondition and receipt
  event jointly scope the obligation; keep the API as the named system.
- Preserve the positive amount range, supported currencies, JSON `decision`
  field, 500 ms service-boundary measurement, concurrency scope, and all three
  decision values.
- Ask for amount precision and the observable scope of terminal
  `indeterminate` behavior before marking the candidate ready.
- Treat the host metadata as metadata, not as a reason to invent a lifecycle.
- Do not claim approval or use ProtoBot-specific terminology.
