# Requirements Elicitation Package

## Understanding

**Requested outcome:** When an operator runs `sync --dry-run` with a valid project path, the command-line tool must list the files that would be changed and must leave every file in the project unmodified. The dry-run option is explicitly defined as preview-only behavior.

**Proposed system boundary:** The command-line tool (`sync` command) — the boundary at which commands are invoked and output is produced. Observable behavior is the content written to the tool's output channel and the state of files on disk after the command returns.

**Actors identified:** Operator (the human or automated process that invokes the command).

**Source language interpretation:**
- `shall` — used in the source; preserved as normative.
- `report` — interpreted as producing observable output at the command's standard output or equivalent channel; the exact channel is unresolved (see Q1).
- `valid project path` — an unresolved domain term (see Q2).
- `files that would change` — unresolved as to format and completeness (see Q3, Q4).
- `shall not modify the project` — normative prohibition; scope of "project" is unresolved (see Q5).

**Source statements sorted by kind:**

| Kind | Statement |
|---|---|
| Behavioral obligation | Report the files that would change when `sync --dry-run` is invoked with a valid project path. |
| Behavioral obligation | Do not modify the project when `sync --dry-run` is invoked. |
| Constraint | The dry-run option is defined as preview-only behavior (explicitly stated by the requester). |
| Design idea | The mechanism by which files are reported is not specified; `report` is a design-neutral verb here. |
| Assumption | "Valid project path" is a domain precondition; its definition is not stated in the source. |

Two independently testable obligations are identified and drafted as separate candidates.

---

## Clarifying Questions

**Q1 — Output channel:** Where does the tool write the file list? Standard output, standard error, a structured log, or a designated output file? Two implementers would choose different channels without this answer.

**Q2 — Valid project path:** What makes a project path "valid"? Is it a path that exists on disk, a path that resolves to a recognized project manifest, something else? This affects both the precondition in Candidate 1 and the boundary cases in both candidates.

**Q3 — Report format:** What is the required format for the file list? One file per line? A structured format (JSON, table)? Must it include relative or absolute paths? Must it include the nature of the change (e.g., added, modified, deleted)?

**Q4 — Completeness of the file list:** Must the list be exhaustive (every file that a live `sync` would change) or is a best-effort or sampled list acceptable? This determines whether a verifier can declare pass or fail.

**Q5 — Scope of "shall not modify":** Does "not modify the project" mean only the files within the project path, or does it also cover adjacent state such as lock files, caches, configuration outside the project path, or network-side state? Must the tool also leave the project path itself (the directory entry) unchanged?

**Q6 — Exit code:** Is there a required exit code for a successful dry-run execution? A non-zero exit code from a preview invocation could mislead callers.

**Q7 — Empty change set:** If `sync --dry-run` determines that no files would change, what is the required output? Silence, an explicit "no changes" message, or an empty list?

---

## Candidate Requirements

### Candidate 1

- **Status:** needs clarification
- **Template:** Complex (`While <precondition>, when <trigger>, the <system> shall <response>.`)
- **Requirement:** While a valid project path is supplied, when an operator invokes `sync --dry-run`, the sync tool shall report the set of files that would be changed by a live sync operation.
- **Source intent and rationale:** The operator needs to preview the effect of `sync` before committing to it, so the tool must produce an observable list of affected files without executing the actual sync. This is the positive reporting obligation.

- **Implementability contract**
  - System boundary: The `sync` command-line tool, at the boundary where it writes output after command invocation.
  - Responsible system: The sync tool.
  - Actor: Operator (human or automated caller of the command).
  - Affected interfaces: The tool's output channel (exact channel — stdout, stderr, or other — is unresolved; see Q1).
  - Inputs and data definitions: The `--dry-run` flag; a project path argument asserted to be "valid" (definition unresolved; see Q2).
  - Preconditions, state, feature scope, and trigger: Precondition — a valid project path is supplied. Trigger — invocation of `sync --dry-run`.
  - Observable normal, alternate, and failure response: Normal — a list of files is produced at the output channel. Alternate — empty change set behavior is unresolved (Q7). Failure path — behavior when the path is invalid is not in scope for this candidate but is needed as a companion (see Suggested Supporting Requirements).
  - Quantities, units, limits, timing, ordering, and tolerances: Format, completeness, and ordering of the file list are unresolved (Q3, Q4).
  - Domain terms, dependencies, and unresolved decisions: "Valid project path" (Q2); "files that would change" (Q3, Q4); output channel (Q1); empty change set (Q7).

- **Verification contract**
  - Setup, state, input data, and stimulus: A project directory that contains files a live `sync` would change exists. The operator invokes `sync --dry-run <project-path>`.
  - Observable evidence or oracle: The tool's output channel contains an enumeration of file names or paths before the command returns.
  - Expected result: Every file that a live `sync` would change appears in the output.
  - Pass/fail criteria, including scope, thresholds, units, and timing: The output set matches the set of files a live `sync` would change (completeness criterion is unresolved pending Q4). No files outside that set appear (false-positive criterion). Timing is unresolved.
  - Boundary, negative, and failure cases: Empty change set (Q7); invalid path (companion candidate needed).
  - Required data, instrumentation, assessment, or external evidence: A known project state with a predictable change set is needed; a verifier must be able to independently determine which files a live sync would affect.
  - Observable at the named boundary alone: Yes — output is observable at the command's output channel.
  - If no, internal evidence needed and rationale: N/A.

---

### Candidate 2

- **Status:** needs clarification
- **Template:** Complex (`While <precondition>, when <trigger>, the <system> shall <response>.`)
- **Requirement:** While a valid project path is supplied, when an operator invokes `sync --dry-run`, the sync tool shall not modify any file within the project.
- **Source intent and rationale:** The dry-run option is explicitly defined as preview-only. The tool must guarantee that no file state is altered, so operators can safely preview in production or sensitive environments.

- **Implementability contract**
  - System boundary: The `sync` command-line tool; observable at the filesystem state of the project path before and after invocation.
  - Responsible system: The sync tool.
  - Actor: Operator.
  - Affected interfaces: The filesystem at the project path. Adjacent state (lock files, caches, external or network-side state) is unresolved (Q5).
  - Inputs and data definitions: The `--dry-run` flag; the project path (definition of "valid" unresolved; see Q2).
  - Preconditions, state, feature scope, and trigger: Precondition — a valid project path is supplied. Trigger — invocation of `sync --dry-run`.
  - Observable normal, alternate, and failure response: Normal — the filesystem state of every file within the project path is identical before and after the command executes. Alternate and failure — unresolved for adjacent or external state (Q5).
  - Quantities, units, limits, timing, ordering, and tolerances: The non-modification guarantee must hold for the complete duration of the command's execution (not only at exit). Timing is otherwise unresolved.
  - Domain terms, dependencies, and unresolved decisions: Scope of "project" (Q5); "valid project path" (Q2); whether adjacent state such as caches or lock files is covered.

- **Verification contract**
  - Setup, state, input data, and stimulus: Record a cryptographic digest or snapshot of every file within the project path before invocation. Invoke `sync --dry-run <project-path>`. Record digests again after the command returns.
  - Observable evidence or oracle: Filesystem state (file contents, metadata, and directory entries) of the project path before and after invocation.
  - Expected result: Every digest recorded after invocation matches the corresponding digest recorded before invocation.
  - Pass/fail criteria, including scope, thresholds, units, and timing: Any difference in file content, metadata, or directory entries within the project path is a failure. The scope of "project path" must be resolved (Q5) to determine whether adjacent state is in scope.
  - Boundary, negative, and failure cases: A file that a live sync would overwrite; a file that a live sync would delete; a newly created file that a live sync would add; concurrent filesystem access during dry-run.
  - Required data, instrumentation, assessment, or external evidence: Filesystem snapshot capability before and after command execution.
  - Observable at the named boundary alone: Yes — observable at the filesystem boundary.
  - If no, internal evidence needed and rationale: N/A.

---

## Suggested Supporting Requirements

**SR-1 — Invalid project path response (Failure-path companion to Candidate 1 and Candidate 2)**
Source behavior: Both candidates are scoped to a "valid" project path. Missing behavior: The tool's response when an invalid path is supplied is undefined. If an implementer must handle this case (and no other requirement addresses it), the tool's behavior — error message, exit code, and absence of output — is unspecified. Reason: Without this, two implementers will produce visibly different behavior for an invalid path.

**SR-2 — Exit code for successful dry-run (Required companion to Candidate 1)**
Source behavior: Candidate 1 requires the tool to report files. Missing behavior: The exit code the tool returns after a successful dry-run is not specified. Reason: Callers in scripts or CI pipelines rely on exit codes to distinguish success from failure; an unspecified exit code makes the command unfit for automation without guessing.

**SR-3 — Empty change set output (Boundary companion to Candidate 1)**
Source behavior: Candidate 1 requires reporting files that would change. Missing behavior: When no files would change, the required output is undefined (silence, an explicit message, an empty list). Reason: A verifier cannot write a complete pass/fail test without knowing the expected output for an empty change set, and an operator cannot distinguish "nothing to do" from a silent failure.

**SR-4 — Idempotence of repeated dry-run invocations (Operational companion to Candidate 2)**
Source behavior: Candidate 2 requires that no file be modified. Missing behavior: Whether two successive `sync --dry-run` invocations produce identical output and leave identical filesystem state is not specified. Reason: Preview-only semantics imply idempotence; if a dry-run changes state that affects the next dry-run's output, the preview guarantee is weakened. Ask whether this is in scope.

---

## Consistency Findings

**Finding 1 — Scope alignment between Candidate 1 and Candidate 2**
Candidates 1 and 2 share the same precondition ("valid project path supplied") and trigger (`sync --dry-run`). They are not in conflict. Both are needed: reporting (C1) and non-modification (C2) are independent, separately testable obligations, consistent with the source requirement that both must hold simultaneously. No conflict.

**Finding 2 — Gap: behavior for invalid project path**
Neither candidate defines what the tool does when the project path is invalid. This is a gap; see SR-1. If another requirement in the broader set covers this, these candidates can be narrowed. If not, at least one additional candidate is needed.

**Finding 3 — Gap: "files that would change" definition**
The phrase "files that would change" is used as the oracle in the verification contract for Candidate 1, but what constitutes a "change" (add, modify, delete, permission change) and whether the list must be exhaustive are unresolved. Until Q3 and Q4 are answered, the verification contract for Candidate 1 cannot reach `ready for review`.

**Finding 4 — Potential overlap: non-modification scope and reporting**
Candidate 2 prohibits modification of files within the project. If "reporting" in Candidate 1 causes the tool to write a file inside the project path (e.g., a log or manifest), that would violate Candidate 2. This is a precedence question: the output channel in Candidate 1 must not be a file within the project path, or an explicit exception is needed. Resolution: confirm (Q1) that output goes to standard output, standard error, or a path outside the project.

---

## Assumptions

**A1:** "Valid project path" is a precondition the caller is expected to satisfy, not a property the tool validates internally. This assumption affects whether Candidate 1 and 2 need an invalid-path branch or whether that is delegated to a separate requirement. Marked provisional; ask Q2.

**A2:** The `sync` tool is a single named system responsible for both obligations. If `sync` delegates to sub-processes or plugins that could independently modify files, the non-modification guarantee in Candidate 2 must extend to those components. This is not addressed in the source.

**A3:** "Modify" in Candidate 2 includes content changes, metadata changes (timestamps, permissions), and structural changes (file creation, deletion, rename). If "modify" means only content changes, the scope narrows and must be stated.

**A4:** The source phrase "report the files that would change" implies the list is deterministic and complete for a given project state, not a sample or estimate. Marked provisional; ask Q4.

---

## Rationale and Context

The source explicitly defines `--dry-run` as "preview-only behavior," establishing a deliberate policy constraint rather than an incidental feature. This framing suggests the non-modification guarantee (Candidate 2) is a safety property — operators may invoke `sync --dry-run` in environments where unintended changes are harmful. The reporting obligation (Candidate 1) supports informed decision-making before committing to a live sync.

The two obligations are separated because they are independently testable and could, in principle, be negotiated separately. The source couples them in a single invocation, so they share the same precondition and trigger; however, a verifier must test each independently (one for output content, one for filesystem state).

No performance, security, privacy, or localization requirements are implied by the source for this feature. These areas are not addressed unless the user confirms they are in scope.

---

## Review Status

| Candidate | Status | Blocking items |
|---|---|---|
| Candidate 1 — Report files that would change | needs clarification | Q1 (output channel), Q2 (valid project path), Q3 (report format), Q4 (completeness), Q7 (empty change set) |
| Candidate 2 — Shall not modify the project | needs clarification | Q2 (valid project path), Q5 (scope of "project"), Finding 4 (output channel must not be inside project path) |

Neither candidate has reached `ready for review`. Both contracts have material unresolved items. Once Q1–Q5 and Q7 are answered and Finding 4 is resolved, both candidates can be revised and re-assessed against the readiness gate.
