// Package jobsite implements the backend-neutral Worker/Triage fixture,
// projection isolation, and the jobsite-sandbox/v1 local test adapter.
// RunFixtureLoop exports fresh Worker A, Worker B, and private Integration
// repositories, imports role-restricted PatchBundle v1 contributions,
// preserves merge history, executes fixture tests, and routes only
// SanitizeFeedback output to the affected Workers. It represents test,
// implementation, both-fault, and specification-gap decisions without
// requiring real model execution or extending the Drafting Table Source
// Control Manager face.
//
// MaxRetries excludes the initial attempt. The authoritative LoopWMS
// boundary moves passing work from building to inspecting and specification
// gaps to blocked. Exhausting retries ends the fixture with an escalation
// while preserving building; it does not authorize abandonment. Invalid
// patches, sanitizer inputs, sandbox operations, or WMS decisions fail
// closed. Private hash-linked cycle evidence retains raw inputs, redaction
// decisions, sanitized feedback, cleanup audits, and lifecycle transitions.
//
// The reusable conformance suite validates adapter API behavior; this
// suite alone is insufficient to enable autonomous execution until an
// integration-level check exercises host/peer access from inside the
// Worker execution boundary. Its reports therefore leave both
// integration_boundary_verified and autonomous_execution false. Real model
// execution and Fullsend adapter integration remain later work (#172).
package jobsite
