// Package validation implements the backend-neutral Validation Rules
// evaluator used by WMS write boundaries and advisory preflight callers.
package validation

import "time"

// RuleVersion identifies the immutable MVP lifecycle ruleset.
const RuleVersion = "validation-rules/v1"

// State is a canonical work-item lifecycle state.
type State string

const (
	StateInitial          State = "initial"
	StateWaiting          State = "waiting"
	StateReadyForBuilding State = "ready-for-building"
	StateBuilding         State = "building"
	StateInspecting       State = "inspecting"
	StateBlocked          State = "blocked"
	StateMerging          State = "merging"
	StateCompleted        State = "completed"
	StateAbandoned        State = "abandoned"
)

// Operation is a canonical lifecycle command or WMS action name.
type Operation string

const (
	OperationMaterialize         Operation = "materialize"
	OperationRefreshDependencies Operation = "refresh-dependencies"
	OperationRevalidate          Operation = "revalidate"
	OperationResolveBlock        Operation = "resolve-block"
	OperationClaim               Operation = "claim"
	OperationRenewLease          Operation = "renew-lease"
	OperationTestsPass           Operation = "tests-pass"
	OperationRaiseSpecQuestion   Operation = "raise-spec-question"
	OperationRefreshActive       Operation = "refresh-active"
	OperationReturnToBuilding    Operation = "return-to-building"
	OperationBeginMerge          Operation = "begin-merge"
	OperationMergeConflict       Operation = "merge-conflict"
	OperationMergeNotApplied     Operation = "merge-not-applied"
	OperationRecordMerge         Operation = "record-merge"
	OperationRecoverLease        Operation = "recover-lease"
	OperationAbandon             Operation = "abandon"
	OperationLifecyclePreflight  Operation = "lifecycle.preflight"
)

// Role is a Gate-issued lifecycle authorization role.
type Role string

const (
	RoleHumanMaintainer Role = "human-maintainer"
	RoleDraftingTable   Role = "drafting-table"
	RoleMaterializer    Role = "materializer"
	RoleJobSite         Role = "job-site"
	RoleReconciler      Role = "reconciler"
)

// Authority distinguishes advisory evaluation from the WMS write boundary.
type Authority string

const (
	AuthorityPreflight     Authority = "preflight"
	AuthorityAuthoritative Authority = "authoritative"
)

// Outcome is the result of one Validation Rules decision.
type Outcome string

const (
	OutcomeAllowed  Outcome = "allowed"
	OutcomeRejected Outcome = "rejected"
	OutcomeOmitted  Outcome = "omitted"
)

// MaterializationOutcome is the reserved result stored for create-or-return.
type MaterializationOutcome string

const (
	MaterializationOutcomeWaiting          MaterializationOutcome = "waiting"
	MaterializationOutcomeReadyForBuilding MaterializationOutcome = "ready-for-building"
	MaterializationOutcomeBlocked          MaterializationOutcome = "blocked"
	MaterializationOutcomeOmitted          MaterializationOutcome = "omitted"
)

// ApprovalStatus is the lifecycle state of one Gate-owned approval.
type ApprovalStatus string

const (
	ApprovalStatusUnused   ApprovalStatus = "unused"
	ApprovalStatusConsumed ApprovalStatus = "consumed"
	ApprovalStatusRevoked  ApprovalStatus = "revoked"
)

// ResolutionSubmissionStatus is the lifecycle state of one blocked-work
// resolution submission.
type ResolutionSubmissionStatus string

const (
	ResolutionSubmissionStatusPending    ResolutionSubmissionStatus = "pending"
	ResolutionSubmissionStatusSuperseded ResolutionSubmissionStatus = "superseded"
	ResolutionSubmissionStatusConsumed   ResolutionSubmissionStatus = "consumed"
)

const (
	CodeUnauthorizedAction   = "UNAUTHORIZED_ACTION"
	CodeInvalidChangeType    = "INVALID_CHANGE_TYPE"
	CodeInvalidTransition    = "INVALID_TRANSITION"
	CodePreconditionFailed   = "PRECONDITION_FAILED"
	CodeStaleState           = "STALE_STATE"
	CodeStaleContractVersion = "STALE_CONTRACT_VERSION"
	CodeStaleFencingToken    = "STALE_FENCING_TOKEN"
	CodeDuplicateClaim       = "DUPLICATE_CLAIM"
	CodeIdempotencyConflict  = "IDEMPOTENCY_CONFLICT"
	CodeAlreadyTerminal      = "ALREADY_TERMINAL"
	CodeNotFound             = "NOT_FOUND"
)

const (
	RetryNever     = "never"
	RetryAuthorize = "authorize"
	RetryRefresh   = "refresh"
	RetryReconcile = "reconcile"
	RetryQuery     = "query"
	RetryNewKey    = "new-key"
)

// AuthorizationContext is trusted Gate output. Callers must not construct it
// from claims in an untrusted operation request.
type AuthorizationContext struct {
	Subject        string      `json:"subject"`
	Role           Role        `json:"role"`
	ProjectID      string      `json:"project_id"`
	WorkItemID     string      `json:"work_item_id,omitempty"`
	ChangeSetID    string      `json:"change_set_id,omitempty"`
	AllowedActions []Operation `json:"allowed_actions"`
	AllowedRefs    []string    `json:"allowed_refs"`
	ExpiresAt      time.Time   `json:"expires_at"`
	PolicyVersion  string      `json:"policy_version"`
}

// Dependency is a trusted WMS-observed work-item dependency.
type Dependency struct {
	ID    string `json:"id"`
	State State  `json:"state"`
}

// Readiness records the validation evidence needed before an item can be
// dispatched to Building.
type Readiness struct {
	ContractComplete           bool     `json:"contract_complete"`
	SourceImmutable            bool     `json:"source_immutable"`
	SpecificationValidated     bool     `json:"specification_validated"`
	RequirementReferencesValid bool     `json:"requirement_references_valid"`
	PipelineEntrySatisfied     bool     `json:"pipeline_entry_satisfied"`
	ImpactDispositioned        bool     `json:"impact_dispositioned"`
	PolicyCompatible           bool     `json:"policy_compatible"`
	UnresolvedReasons          []string `json:"unresolved_reasons,omitempty"`
}

// WorkItem is the WMS-owned current lifecycle record supplied to the
// evaluator. It contains no backend-specific issue or card fields.
type WorkItem struct {
	ID                           string                 `json:"id"`
	ProjectID                    string                 `json:"project_id"`
	State                        State                  `json:"state"`
	ContractVersion              uint64                 `json:"contract_version"`
	MaterializationKey           string                 `json:"materialization_key,omitempty"`
	ChangeType                   string                 `json:"change_type,omitempty"`
	ImplementationRequired       bool                   `json:"implementation_required"`
	Priority                     string                 `json:"priority,omitempty"`
	Owner                        string                 `json:"owner,omitempty"`
	Lease                        *Lease                 `json:"lease,omitempty"`
	Dependencies                 []Dependency           `json:"dependencies,omitempty"`
	Readiness                    Readiness              `json:"readiness"`
	BlockReason                  string                 `json:"block_reason,omitempty"`
	ActiveResolutionSubmissionID string                 `json:"active_resolution_submission_id,omitempty"`
	InspectionRunSealed          bool                   `json:"inspection_run_sealed"`
	FindingsTerminal             bool                   `json:"findings_terminal"`
	FinalTestsPassed             bool                   `json:"final_tests_passed"`
	ExpectedMerge                *MergeEnvelope         `json:"expected_merge,omitempty"`
	Reconciliation               ReconciliationEvidence `json:"reconciliation"`
}

// Lease identifies the current owner and fencing token.
type Lease struct {
	Owner        string    `json:"owner"`
	FencingToken string    `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// MergeEnvelope binds completion to the tested candidate and integration
// result observed by the WMS.
type MergeEnvelope struct {
	ProductTreeDigest string `json:"product_tree_digest"`
	InspectionRunID   string `json:"inspection_run_id"`
	IntegrationHead   string `json:"integration_head"`
	Target            string `json:"target"`
	ContractVersion   uint64 `json:"contract_version"`
	MergeCommit       string `json:"merge_commit"`
}

// ReconciliationEvidence is populated from trusted WMS/Git observations,
// never from a caller's proof fields.
type ReconciliationEvidence struct {
	Status        string         `json:"status,omitempty"`
	GitMutation   string         `json:"git_mutation,omitempty"`
	MergeEnvelope *MergeEnvelope `json:"merge_envelope,omitempty"`
}

// Payload contains command-specific, typed lifecycle data.
type Payload struct {
	WorkItem               *WorkItem      `json:"work_item,omitempty"`
	MaterializationKey     string         `json:"materialization_key,omitempty"`
	ChangeType             string         `json:"change_type,omitempty"`
	ChangeSetID            string         `json:"change_set_id,omitempty"`
	HumanApprovalID        string         `json:"human_approval_id,omitempty"`
	ApprovalDigest         string         `json:"approval_resolution_digest,omitempty"`
	ResolutionKind         string         `json:"resolution_kind,omitempty"`
	ResolutionSubmissionID string         `json:"resolution_submission_id,omitempty"`
	BuildTestsPassed       bool           `json:"build_tests_passed,omitempty"`
	Question               string         `json:"question,omitempty"`
	InspectionRunSealed    bool           `json:"inspection_run_sealed,omitempty"`
	FindingsTerminal       bool           `json:"findings_terminal,omitempty"`
	FinalTestsPassed       bool           `json:"final_tests_passed,omitempty"`
	InContractDefect       bool           `json:"in_contract_defect,omitempty"`
	FinalTestsFailed       bool           `json:"final_tests_failed,omitempty"`
	CancellationConfirmed  bool           `json:"cancellation_confirmed,omitempty"`
	MergeEnvelope          *MergeEnvelope `json:"merge_envelope,omitempty"`
}

// Request is one normalized lifecycle command. Authorization is supplied by
// the trusted WMS/Gate boundary, separately from transport input.
type Request struct {
	Operation               Operation            `json:"operation"`
	ProjectID               string               `json:"project_id"`
	WorkItemID              string               `json:"work_item_id,omitempty"`
	MaterializationKey      string               `json:"materialization_key,omitempty"`
	IdempotencyKey          string               `json:"idempotency_key,omitempty"`
	ExpectedState           State                `json:"expected_state,omitempty"`
	ExpectedContractVersion *uint64              `json:"expected_contract_version,omitempty"`
	FencingToken            string               `json:"fencing_token,omitempty"`
	References              []string             `json:"references,omitempty"`
	Payload                 Payload              `json:"payload"`
	Authorization           AuthorizationContext `json:"authorization"`
}

// ApprovalRecord is Gate-owned single-use approval state.
type ApprovalRecord struct {
	ID                      string         `json:"id,omitempty"`
	ApprovedSubject         string         `json:"approved_subject"`
	DelegatedPrincipal      string         `json:"delegated_principal"`
	ProjectID               string         `json:"project_id,omitempty"`
	WorkItemID              string         `json:"work_item_id,omitempty"`
	RequestID               string         `json:"request_id,omitempty"`
	Digest                  string         `json:"resolution_digest"`
	Action                  Operation      `json:"action"`
	ResolutionKind          string         `json:"resolution_kind,omitempty"`
	ExpectedState           State          `json:"expected_state,omitempty"`
	ExpectedContractVersion *uint64        `json:"expected_contract_version,omitempty"`
	PolicyVersion           string         `json:"policy_version,omitempty"`
	ExpiresAt               time.Time      `json:"expires_at"`
	Status                  ApprovalStatus `json:"status"`
}

// ApprovalRequirement describes the exact Gate binding a mutation consumes.
type ApprovalRequirement struct {
	Action                  Operation
	DelegatedPrincipal      string
	ProjectID               string
	WorkItemID              string
	RequestID               string
	ResolutionKind          string
	Digest                  string
	ExpectedState           State
	ExpectedContractVersion *uint64
	PolicyVersion           string
}

// ResolutionSubmission is the WMS-owned record submitted for a later
// Materializer transition.
type ResolutionSubmission struct {
	ID                                 string                     `json:"id"`
	WorkItemID                         string                     `json:"work_item_id"`
	Kind                               string                     `json:"kind"`
	ChangeSetID                        string                     `json:"change_set_id,omitempty"`
	ApprovalID                         string                     `json:"approval_id"`
	ApprovalDigest                     string                     `json:"approval_digest"`
	ApprovedHumanSubject               string                     `json:"approved_human_subject"`
	Status                             ResolutionSubmissionStatus `json:"status"`
	PlannedDependencyComplete          bool                       `json:"planned_dependency_complete"`
	IndependentInspectorConfirmed      bool                       `json:"independent_inspector_confirmed"`
	IndependentInspectorConfirmationID string                     `json:"independent_inspector_confirmation_id,omitempty"`
}

// ResolutionPreview is hypothetical preflight data; it never becomes a
// persisted work-item dependency.
type ResolutionPreview struct {
	Kind                      string
	ChangeSetID               string
	PlannedDependencyComplete bool
	InspectorConfirmed        bool
}

// EvaluationContext contains normalized time, authority, and WMS/Gate state
// that the pure evaluator may trust for this one decision.
type EvaluationContext struct {
	Authority             Authority
	EvaluationTime        time.Time
	LeaseDuration         time.Duration
	NextFencingToken      string
	Approvals             map[string]ApprovalRecord
	ResolutionSubmissions map[string]ResolutionSubmission
	PreviewResolution     *ResolutionPreview
	RefreshReadiness      *Readiness
	RefreshDependencies   *[]Dependency
}

// StateVersion identifies one observed or proposed lifecycle revision.
type StateVersion struct {
	State           State  `json:"state"`
	ContractVersion uint64 `json:"contract_version"`
}

// MaterializationReservation records the create-or-return result for a
// logical work item, including an omitted result.
type MaterializationReservation struct {
	Key               string                 `json:"materialization_key"`
	SourceFingerprint string                 `json:"source_fingerprint"`
	Outcome           MaterializationOutcome `json:"outcome"`
}

// Decision is the stable result of preflight or authoritative evaluation.
type Decision struct {
	Outcome                    Outcome                     `json:"outcome"`
	Replayed                   bool                        `json:"replayed"`
	Authority                  Authority                   `json:"authority"`
	RuleVersion                string                      `json:"rule_version"`
	PolicyVersion              string                      `json:"policy_version"`
	Operation                  Operation                   `json:"operation"`
	Before                     *StateVersion               `json:"before,omitempty"`
	After                      *StateVersion               `json:"after,omitempty"`
	MaterializationReservation *MaterializationReservation `json:"materialization_reservation,omitempty"`
	FencingTokenIssued         string                      `json:"fencing_token_issued,omitempty"`
	Rejection                  *Rejection                  `json:"rejection,omitempty"`
}

// Rejection is a stable, actionable machine-readable rule failure.
type Rejection struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	Retry   string         `json:"retry"`
}
