// Package ears reads the governed specification boundary through
// `ears-manager --output json`, with argument lists. The SCM never writes
// through it.
//
// The command and envelope shapes are those of
// docs/architecture/ears-manager-cli.md. The full `change-set show` contract
// has six data keys: `change_set`, `status`, `changed_count`,
// `applicable_count`, `manifest_path`, and `paths`. The SCM consumes four:
// `change_set`, `status`, `manifest_path`, and `paths`.
//
//   - `paths` is the exact file set of the change set, its manifest included.
//   - `check`: status 4 with `artifact.digest_mismatch` diagnostics whose
//     `path` names the registry file `.protobot/project.yaml`, and status 5
//     for an incomplete or stale impact assessment.
//   - `.protobot/projection.yaml` entries use `path` and `class`; the SCM
//     parses those names when identifying change-set-owned classifications.
//   - `change-set compare` entries may include `before` and `after`. Both
//     fields are optional; the current CLI and PR renderer need not emit them.
package ears

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
	"github.com/redhat-et/protobot/source-control-manager/internal/jsonx"
	"github.com/redhat-et/protobot/source-control-manager/internal/result"
)

// Binary is the ears-manager executable, found on PATH.
const Binary = "ears-manager"

// Exit statuses of the #30 contract.
const (
	StatusOK         = 0
	StatusUsage      = 2
	StatusProject    = 3
	StatusValidation = 4
	StatusConflict   = 5
	StatusIO         = 6
	StatusInternal   = 70
)

// Diagnostic is one structured ears-manager diagnostic.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Path     string `json:"path,omitempty"`
	RecordID string `json:"record_id,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// ErrorBody is the error of a failed envelope.
type ErrorBody struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	ExitCode    int          `json:"exit_code"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Mutation    string       `json:"mutation"`
	Retry       string       `json:"retry"`
}

// Envelope is an ears-manager result envelope.
type Envelope struct {
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	Command       string          `json:"command"`
	Data          json.RawMessage `json:"data"`
	Diagnostics   []Diagnostic    `json:"diagnostics"`
	Error         *ErrorBody      `json:"error"`
}

// Call is one ears-manager run.
type Call struct {
	Args   []string
	Status int
	Raw    json.RawMessage
	Env    Envelope
}

// Command returns the argument list that ran.
func (c *Call) Command() []string { return append([]string{Binary}, c.Args...) }

// Diagnostics returns every diagnostic of the envelope, error ones first.
func (c *Call) Diagnostics() []Diagnostic {
	var out []Diagnostic
	if c.Env.Error != nil {
		out = append(out, c.Env.Error.Diagnostics...)
	}
	return append(out, c.Env.Diagnostics...)
}

// Codes returns the sorted, distinct diagnostic codes of the envelope.
func (c *Call) Codes() []string {
	seen := map[string]bool{}
	codes := []string{}
	for _, d := range c.Diagnostics() {
		if d.Code != "" && !seen[d.Code] {
			seen[d.Code] = true
			codes = append(codes, d.Code)
		}
	}
	sort.Strings(codes)
	return codes
}

// Client runs ears-manager in the working-tree root.
type Client struct {
	Dir string
}

// Run runs `ears-manager --output json <args>`. A failure is returned only
// when ears-manager did not run or did not answer with one JSON document;
// every exit status is the caller's to judge.
func (c *Client) Run(args ...string) (*Call, *result.Failure) {
	argv := append([]string{"--output", "json"}, args...)
	call := &Call{Args: argv}
	path, err := exec.LookPath(Binary)
	if err != nil {
		return call, result.Fail(result.SpecToolFailed, "ears-manager did not run.",
			jsonx.F("command", call.Command()), jsonx.F("reason", "ears-manager was not found on PATH"))
	}
	cmd, cancel := gitx.Command(path, argv...)
	defer cancel()
	cmd.Dir = c.Dir
	cmd.Env = gitx.Environ(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return call, result.Fail(result.SpecToolFailed, "ears-manager did not run.",
				jsonx.F("command", call.Command()), jsonx.F("reason", "ears-manager could not start"))
		}
		call.Status = exitErr.ExitCode()
	}
	raw := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(raw) || json.Unmarshal(raw, &call.Env) != nil {
		return call, result.Fail(result.SpecToolFailed, "ears-manager did not answer with one JSON document.",
			jsonx.F("command", call.Command()), jsonx.F("status", call.Status))
	}
	call.Raw = json.RawMessage(raw)
	return call, nil
}

// ToolFailure is SPEC_TOOL_FAILED for a read that returned status 2, 3, 6,
// or 70, or any status the read does not expect.
func ToolFailure(call *Call) *result.Failure {
	f := result.Fail(result.SpecToolFailed, "ears-manager failed on a read.",
		jsonx.F("command", call.Command()), jsonx.F("status", call.Status), jsonx.F("envelope", call.Raw))
	if call.Env.Error != nil && call.Env.Error.Mutation == result.MutationUnknown {
		f = f.WithRetry(result.RetryReconcile)
	}
	return f
}

// ToolStatus reports a status that always means SPEC_TOOL_FAILED on a read.
func ToolStatus(status int) bool {
	switch status {
	case StatusUsage, StatusProject, StatusIO, StatusInternal:
		return true
	}
	return false
}

// Operation is one entry of a manifest operation list.
type Operation struct {
	Action        string `json:"action"`
	RequirementID string `json:"requirement_id,omitempty"`
	InterfaceID   string `json:"interface_id,omitempty"`
	ArtifactID    string `json:"artifact_id,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
}

// Assessment is one recorded impact disposition.
type Assessment struct {
	RequirementID string `json:"requirement_id"`
	Disposition   string `json:"disposition"`
	Rationale     string `json:"rationale"`
	Origin        string `json:"origin"`
}

// Manifest is the part of a change-set manifest that the SCM reads.
type Manifest struct {
	ID                      string       `json:"id"`
	BaseCommit              string       `json:"base_commit"`
	Intent                  string       `json:"intent"`
	ImplementationRequired  bool         `json:"implementation_required"`
	ImplementationRationale string       `json:"implementation_rationale"`
	ImpactAssessment        []Assessment `json:"impact_assessment"`
}

// Show is the data of `change-set show`.
type Show struct {
	ChangeSet    Manifest `json:"change_set"`
	Status       string   `json:"status"`
	ManifestPath string   `json:"manifest_path"`
	Paths        []string `json:"paths"`
}

// ShowChangeSet reads a change set, at a commit when at is not empty. It
// returns nil data and no failure when no manifest of the change set
// exists there.
func (c *Client) ShowChangeSet(id, at string) (*Show, *Call, *result.Failure) {
	args := []string{"change-set", "show", "--change-set", id}
	if at != "" {
		args = append(args, "--at", at)
	}
	call, failure := c.Run(args...)
	if failure != nil {
		return nil, call, failure
	}
	if call.Status == StatusOK && call.Env.OK {
		var show Show
		if err := json.Unmarshal(call.Env.Data, &show); err != nil {
			return nil, call, ToolFailure(call)
		}
		return &show, call, nil
	}
	if call.Env.Error != nil && call.Env.Error.Code == "change_set.not_found" && !ToolStatus(call.Status) {
		return nil, call, nil
	}
	return nil, call, ToolFailure(call)
}

// Changed is one entry of the `changed` list of `change-set compare`.
// Before and After are optional result fields and remain raw JSON because
// compare values may have different shapes.
type Changed struct {
	Action        string          `json:"action"`
	RequirementID string          `json:"requirement_id"`
	InterfaceID   string          `json:"interface_id"`
	ArtifactID    string          `json:"artifact_id"`
	Before        json.RawMessage `json:"before"`
	After         json.RawMessage `json:"after"`
}

// Compare is the data of `change-set compare`.
type Compare struct {
	ChangeSetID            string    `json:"change_set_id"`
	AgainstCommit          string    `json:"against_commit"`
	Changed                []Changed `json:"changed"`
	ImplementationRequired *bool     `json:"implementation_required"`
}

// CompareChangeSet runs `change-set compare`.
func (c *Client) CompareChangeSet(id string) (*Compare, *result.Failure) {
	call, failure := c.Run("change-set", "compare", "--change-set", id)
	if failure != nil {
		return nil, failure
	}
	if call.Status != StatusOK || !call.Env.OK {
		return nil, ToolFailure(call)
	}
	var data Compare
	if err := json.Unmarshal(call.Env.Data, &data); err != nil {
		return nil, ToolFailure(call)
	}
	return &data, nil
}

// Candidate is one impact candidate.
type Candidate struct {
	RequirementID          string   `json:"requirement_id"`
	Origin                 string   `json:"origin"`
	MatchedBy              []string `json:"matched_by"`
	RecommendedDisposition string   `json:"recommended_disposition"`
	RecordedDisposition    *string  `json:"recorded_disposition"`
	Rationale              *string  `json:"rationale"`
}

// Impact is the data of `impact`.
type Impact struct {
	ChangeSetID      string      `json:"change_set_id"`
	Candidates       []Candidate `json:"candidates"`
	AssessmentStatus string      `json:"assessment_status"`
}

// ImpactOf runs `impact`.
func (c *Client) ImpactOf(id string) (*Impact, *result.Failure) {
	call, failure := c.Run("impact", "--change-set", id)
	if failure != nil {
		return nil, failure
	}
	if call.Status != StatusOK || !call.Env.OK {
		return nil, ToolFailure(call)
	}
	var data Impact
	if err := json.Unmarshal(call.Env.Data, &data); err != nil {
		return nil, ToolFailure(call)
	}
	return &data, nil
}

// Check runs `check --change-set <id>`. Every exit status is returned for
// the caller to judge.
func (c *Client) Check(id string) (*Call, *result.Failure) {
	return c.Run("check", "--change-set", id)
}
