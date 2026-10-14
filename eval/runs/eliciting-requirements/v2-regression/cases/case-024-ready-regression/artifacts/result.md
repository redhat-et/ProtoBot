# Requirements Elicitation Package

## Understanding

**Requested outcome.** Any unauthenticated HTTP client that sends `GET /live` to the Health API shall receive an immediate, deterministic liveness signal confirming the API process is running.

**System boundary.** The unauthenticated `GET /live` endpoint of the Health API. The measurement boundary is the API process boundary: from receipt of complete request headers to emission of the final response byte. Internal components, reverse proxies, load balancers, and network transit are outside scope.

**Actor.** Any client (no authentication required).

**Scope of this package.** Three independent observable obligations are present in the source: (1) the response status and body content; (2) the response Content-Type header; (3) the latency ceiling under a bounded concurrency ceiling. Each is drafted as a separate candidate because they can be negotiated and verified independently.

**Explicitly out of scope per source.** Non-GET requests, request bodies, error responses, and behavior above 25 concurrent requests in flight are specified separately and are not addressed here.

---

## Clarifying Questions

No material questions remain for the three candidates below. All quantities, units, trigger events, boundaries, and observability conditions are fully specified by the source. The candidates are `ready for review`.

The following questions are noted as **optional considerations** for future scope expansion, not blockers for the current candidates:

1. **Idle / keepalive behavior.** Does the 100 ms ceiling apply to a pipelined request on a keepalive connection, or only to a fresh TCP connection? The source says "receipt of complete request headers," which is sufficient for measurement, but pipeline ordering may affect instrumentation guidance.
2. **Clock source.** Is the measurement clock wall-clock time at the process boundary, or must a monotonic clock be used? The source is silent; both are common practice and verification method may need to name one.
3. **Concurrency counting scope.** Does "in flight" count requests across all process workers, or per-process-instance? This matters for multi-worker deployments. If deployment is single-process, this is not applicable.

---

## Candidate Requirements

### Candidate 1

- **Status:** ready for review
- **Template:** Event-driven
- **Requirement:** When a client sends an HTTP GET request to `/live` with no request body, the Health API shall return HTTP status code 200 with a response body containing exactly the UTF-8 byte sequence `{"status":"live"}`.

**Source intent and rationale.**
The source specifies a deterministic liveness probe. Any client (monitoring agent, load balancer, orchestrator) must be able to confirm the API process is alive by inspecting the status code and the exact body content. The exact byte sequence requirement prevents whitespace variants or field-order variants from silently satisfying verification.

**Implementability contract**
- System boundary: The unauthenticated `GET /live` endpoint of the Health API, at the API process boundary.
- Responsible system: The Health API.
- Actor: Any client; no authentication or request body required.
- Affected interfaces: HTTP interface at the `/live` path; inbound request, outbound response.
- Inputs and data definitions: HTTP GET method; path `/live`; no request body. Request body is defined as absent (content-length zero or no body framing).
- Preconditions, state, feature scope, and trigger: No precondition. Trigger is receipt of a complete HTTP GET request to `/live` with no request body.
- Observable normal, alternate, and failure response: HTTP 200 status code; response body is exactly the UTF-8 byte sequence `{"status":"live"}` (16 bytes: `{`, `"`, `s`, `t`, `a`, `t`, `u`, `s`, `"`, `:`, `"`, `l`, `i`, `v`, `e`, `"`, `}`). No other body content, no BOM, no trailing newline unless the exact byte sequence is defined to include one — the source states the sequence explicitly so no variation is permitted.
- Quantities, units, limits, timing, ordering, and tolerances: Exact byte match; no tolerance. Byte count is fixed at the literal encoding of `{"status":"live"}` in UTF-8.
- Domain terms, dependencies, and unresolved decisions: "No request body" means zero content bytes and no `Content-Length` header with a positive value, or no body framing. "Exactly the UTF-8 byte sequence" excludes BOM, pretty-printing, alternate field order, and trailing whitespace. No unresolved decisions for this candidate.

**Verification contract**
- Setup, state, input data, and stimulus: Send an HTTP GET request to the API process at path `/live` with no request body. No authentication headers required.
- Observable evidence or oracle: Inspect the HTTP response status code and the raw response body bytes at the API process boundary.
- Expected result: Status code is `200`; raw body bytes match exactly `{"status":"live"}` in UTF-8 with no additional bytes.
- Pass/fail criteria, including scope, thresholds, units, and timing: Pass if and only if status code equals `200` AND body bytes equal the 16-byte UTF-8 encoding of `{"status":"live"}`. Fail if status code differs, body differs by any byte, or extra bytes are present.
- Boundary, negative, and failure cases: Candidate 1 covers the normal path only. Non-GET methods, request bodies, and error conditions are out of scope per the source and specified separately.
- Required data, instrumentation, assessment, or external evidence: An HTTP client capable of capturing raw response bytes; ability to measure at the API process boundary (not at a proxy).
- Observable at the named boundary alone: yes

---

### Candidate 2

- **Status:** ready for review
- **Template:** Event-driven
- **Requirement:** When a client sends an HTTP GET request to `/live` with no request body, the Health API shall include a `Content-Type` response header with the value `application/json`.

**Source intent and rationale.**
The source explicitly requires `Content-Type: application/json` with no parameters. Separating this from Candidate 1 allows independent negotiation (e.g., whether a charset parameter is permitted) and independent verification (header inspection is distinct from body byte inspection).

**Implementability contract**
- System boundary: The unauthenticated `GET /live` endpoint of the Health API, at the API process boundary.
- Responsible system: The Health API.
- Actor: Any client; no authentication or request body required.
- Affected interfaces: HTTP response headers.
- Inputs and data definitions: Same as Candidate 1.
- Preconditions, state, feature scope, and trigger: Same trigger as Candidate 1.
- Observable normal, alternate, and failure response: `Content-Type` header present in the response with value exactly `application/json`. Source specifies "with no parameters," so `application/json; charset=utf-8` is non-conforming.
- Quantities, units, limits, timing, ordering, and tolerances: Header value must equal `application/json` with no parameters appended. Case-insensitivity of HTTP header names is standard; the value `application/json` is case-sensitive per MIME type convention.
- Domain terms, dependencies, and unresolved decisions: "With no parameters" is explicit in the source. No unresolved decisions.

**Verification contract**
- Setup, state, input data, and stimulus: Same as Candidate 1.
- Observable evidence or oracle: Inspect the `Content-Type` response header value in the HTTP response at the API process boundary.
- Expected result: `Content-Type` header is present and its value is `application/json` with no parameters.
- Pass/fail criteria, including scope, thresholds, units, and timing: Pass if and only if `Content-Type` header is present AND its value is exactly `application/json` (no semicolon, no parameters). Fail if header is absent, has a different value, or includes any parameters.
- Boundary, negative, and failure cases: Candidate 2 covers the normal path only. Non-GET methods and error responses are out of scope.
- Required data, instrumentation, assessment, or external evidence: An HTTP client capable of inspecting raw response headers at the API process boundary.
- Observable at the named boundary alone: yes

---

### Candidate 3

- **Status:** ready for review
- **Template:** Complex
- **Requirement:** While no more than 25 GET requests to `/live` are concurrently in flight at the API process boundary, when a client sends an HTTP GET request to `/live` with no request body, the Health API shall emit the final response byte within 100 milliseconds of receiving the complete request headers for that request.

**Source intent and rationale.**
The source specifies a P100 (worst-case, no exceptions) latency ceiling of 100 ms measured at the API process boundary, scoped to a concurrency ceiling of at most 25 simultaneous in-flight requests (exactly 25 is included). This is a complex form because the concurrency state is a genuine precondition: the ceiling applies only while that concurrency bound holds. Above 25 in-flight requests, behavior is specified separately.

**Implementability contract**
- System boundary: The unauthenticated `GET /live` endpoint of the Health API, at the API process boundary.
- Responsible system: The Health API.
- Actor: Any client.
- Affected interfaces: HTTP interface; timing measured from receipt of complete request headers to emission of the final response byte, both at the API process boundary (not at a proxy, client, or network interface).
- Inputs and data definitions: HTTP GET to `/live`; no request body. Concurrency count is the number of such requests whose final response byte has not yet been emitted at the instant the new request's complete headers are received.
- Preconditions, state, feature scope, and trigger: **Precondition (state):** The number of GET `/live` requests concurrently in flight at the API process boundary is greater than or equal to 0 and less than or equal to 25. **Trigger:** Receipt of complete request headers for an HTTP GET to `/live` with no request body.
- Observable normal, alternate, and failure response: The final byte of the HTTP response for that request is emitted at the API process boundary within 100 ms of the moment the complete request headers were received.
- Quantities, units, limits, timing, ordering, and tolerances: Maximum latency: 100 ms, P100 (every request, no statistical exemptions). Concurrency scope: 0 to 25 inclusive. Measurement start: receipt of complete request headers at the API process boundary. Measurement end: emission of final response byte at the API process boundary. No tolerance stated; 100 ms is the ceiling.
- Domain terms, dependencies, and unresolved decisions: "In flight" means a request whose complete headers have been received and whose final response byte has not yet been emitted, both at the API process boundary. "P100" means the ceiling applies to every request without exception under the stated concurrency bound. "API process boundary" excludes reverse proxies, load balancers, and network transit. No unresolved decisions.

**Verification contract**
- Setup, state, input data, and stimulus: Instrument the API process boundary (not a proxy) to record, for each GET `/live` request: (a) the timestamp of receipt of the complete request headers and (b) the timestamp of emission of the final response byte. Send requests such that the concurrency level at the process boundary reaches but does not exceed 25 simultaneous in-flight requests. Include a test at exactly 25 concurrent requests.
- Observable evidence or oracle: Per-request latency = (timestamp b) − (timestamp a), measured at the API process boundary using a consistent clock. Record all latencies under all concurrency levels from 1 to 25.
- Expected result: Every measured latency value is ≤ 100 ms.
- Pass/fail criteria, including scope, thresholds, units, and timing: Pass if and only if every individual request latency, for all requests measured while concurrency is 0–25, is ≤ 100 ms. A single request exceeding 100 ms is a failure. Concurrency must be verified to have stayed within the 0–25 range during the measurement window for each passing request counted.
- Boundary, negative, and failure cases: Include a test at exactly 25 concurrent requests (the stated boundary). Behavior above 25 concurrent requests is out of scope for this candidate and specified separately.
- Required data, instrumentation, assessment, or external evidence: Per-request timestamps recorded at the API process boundary (not at a client or proxy); a monotonic clock or equivalent capable of sub-millisecond resolution; concurrency tracking to confirm the 0–25 range during each measurement window.
- Observable at the named boundary alone: yes

---

## Suggested Supporting Requirements

**SR-1 — Failure-path companion (relates to Candidates 1, 2, 3)**
*Missing behavior:* The source does not specify what the Health API shall return when the `/live` endpoint itself is reachable but an upstream dependency (e.g., database) is degraded. If liveness is strictly "process is running" (not "dependencies are healthy"), this is not needed; if dependency state affects the liveness response, a separate requirement is needed.
*Suggestion:* Confirm whether the liveness response is strictly a process-level signal, independent of all external dependencies, before implementation. If confirmed, a ubiquitous requirement stating that the liveness response depends only on the API process being able to handle HTTP requests (and not on any external system state) would remove implementation ambiguity.

**SR-2 — Boundary companion (relates to Candidate 3)**
*Missing behavior:* The source explicitly defers behavior above 25 concurrent requests to a separate specification. Until that specification exists, an implementer does not know whether to queue, reject (with 429 or 503), or shed load above the threshold. This is recorded as a gap for completeness.
*Suggestion:* Capture the high-concurrency behavior (>25 in-flight) in a companion requirement before implementation proceeds to concurrency-management design.

**SR-3 — Operational companion (relates to Candidate 3)**
*Missing behavior:* The source specifies P100 at ≤25 concurrency, but does not specify a steady-state or average-case latency target, nor a sustained-throughput rate. A very low request rate may satisfy P100 trivially; a high sustained rate may reveal resource exhaustion not captured by a burst test.
*Suggestion (optional consideration):* Consider whether a sustained-rate latency requirement or a request-rate capacity requirement is needed for completeness of the performance specification.

**SR-4 — Interface companion (relates to Candidates 1, 2)**
*Missing behavior:* The source does not specify response behavior for a GET `/live` request that includes a request body. The source states request bodies are "specified separately." This gap is flagged so that implementers know not to infer behavior from Candidates 1 and 2.
*Note:* No action needed in this package; recorded as a known open item in the broader specification.

---

## Consistency Findings

**Finding 1 — No conflict between Candidates 1 and 2.**
Both are triggered by the same event and specify different aspects of the same response (body and header). They are compatible and complementary.

**Finding 2 — No conflict between Candidates 1/2 and Candidate 3.**
Candidate 3 adds a timing constraint on the same response. All three candidates can be simultaneously satisfied. There is no contradiction.

**Finding 3 — Intentional scope gap acknowledged.**
The source explicitly excludes non-GET methods, request bodies, error responses, and concurrency above 25. These are intentional gaps, not conflicts. Candidates 1–3 do not overlap with the out-of-scope behaviors.

**Finding 4 — P100 vs. "maximum" terminology.**
The source uses both "maximum" and "P100" to describe the latency ceiling. These are consistent: P100 means every request (no statistical exemption), which is equivalent to a hard maximum. No conflict.

**Finding 5 — Concurrency boundary at exactly 25.**
The source states "no more than 25 such requests are in flight; exactly 25 is included." Candidate 3 reflects this by using "less than or equal to 25" in the precondition. The boundary value is covered and not ambiguous.

---

## Assumptions

**A1.** "No request body" means the HTTP request carries zero content bytes. Requests with a `Content-Length: 0` header and no body framing are treated as having no request body. This interpretation does not affect Candidates 1 or 2 (which specify the response); it is relevant only if a companion requirement for body-present behavior is added.

**A2.** "Receipt of complete request headers" means the API process has received and parsed all HTTP headers for the request, including the terminal CRLF CRLF sequence (HTTP/1.1) or end-of-headers frame (HTTP/2). This is the start of the measurement window for Candidate 3.

**A3.** "Emission of the final response byte" means the last byte of the HTTP response has been written to the transport layer by the API process (i.e., passed to the OS socket send buffer). Network transmission time to the client is excluded.

**A4.** "API process boundary" means the boundary of the API server process itself, not a reverse proxy, sidecar, or load balancer that may sit in front of it.

**A5.** The word "maximum" in the source is treated as equivalent to P100 (every request, no statistical exemption), consistent with the explicit "P100" qualifier also present in the source.

---

## Rationale and Context

The `GET /live` endpoint is a liveness probe: it signals that the API process is running and able to serve HTTP traffic. Liveness probes are used by orchestrators (e.g., Kubernetes) and load balancers to detect process failure and trigger restarts or traffic removal. The requirements are deliberately minimal and deterministic: a fixed body, a fixed content type, and a hard latency ceiling under normal load. This design ensures the probe is fast, cheap, and unambiguous. The strict byte-level body specification prevents false positives from content negotiation or serialization variants.

---

## Review Status

| Candidate | Status | Blocking items |
|---|---|---|
| Candidate 1 — HTTP 200 and body | ready for review | None |
| Candidate 2 — Content-Type header | ready for review | None |
| Candidate 3 — Latency ceiling at ≤25 concurrency | ready for review | None |

**Unresolved open items (non-blocking for this package):**
- SR-2: High-concurrency behavior (>25 in-flight) is specified separately; that specification is not yet included in this package.
- SR-1: Confirm liveness is strictly process-level (independent of external dependencies).
- SR-3: Consider whether a sustained-rate or throughput requirement is needed.

No candidate in this package is blocked. All three are `ready for review`.
