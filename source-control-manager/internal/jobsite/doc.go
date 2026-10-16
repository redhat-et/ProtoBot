// Package jobsite is the standalone Job Site projection and sandbox
// fixture. It exports Worker A, Worker B, and private Integration
// repositories from a source commit and a version-1 projection policy,
// validates PatchBundle v1 imports, and implements the
// jobsite-sandbox/v1 contract with a local test adapter and reusable
// in-module conformance suite. It does not extend the Drafting Table Source
// Control Manager face. The integration loop is issue #69; Fullsend
// mapping is issue #172.
//
// The reusable conformance suite validates adapter API behavior; this
// suite alone is insufficient to enable autonomous execution until an
// integration-level check exercises host/peer access from inside the
// Worker execution boundary. Its reports therefore leave both
// integration_boundary_verified and autonomous_execution false.
package jobsite
