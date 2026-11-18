// Package testutil provides canonical builders for valid ProtoBot V2
// requirement and interface records used by validator tests.
//
// Baseline fixtures satisfy every check registered in
// specvalidation.Validate. Tests should diverge from that baseline
// only on the property under test.
//
// Requirement options (such as WithID, WithType, WithText, WithStatus,
// WithAppliesTo, and WithRelationships) must not be passed to
// ValidInterfaceDocument or ValidInterfaceYAML. Interface options (such as
// WithName, WithKind, and WithDescription) must not be passed to
// ValidRequirementDocument or ValidRequirementYAML.
package testutil

import (
	"fmt"
	"path"
	"strings"

	"github.com/redhat-et/protobot/validator/internal/layout"
	"github.com/redhat-et/protobot/validator/internal/records"
	"gopkg.in/yaml.v3"
)

const (
	defaultRequirementID        = "REQ-API-00002"
	defaultRequirementText      = "When a client sends GET /health, the service shall return HTTP 200."
	defaultScope                = "api"
	defaultInterfaceName        = "health-api"
	defaultInterfaceKind        = "http"
	defaultInterfaceDescription = "Health check HTTP API used by unauthenticated clients."
)

// Option mutates a canonical V2 test fixture.
type Option func(*fixture)

type fixture struct {
	path        string
	pathSet     bool
	req         records.Requirement
	iface       records.InterfaceRecord
	nilFields   bool
	isInterface bool
}

// ValidRequirementDocument returns a requirement document that satisfies
// every currently registered specification check when validated alone.
// Interface options must not be passed to this builder.
func ValidRequirementDocument(opts ...Option) layout.Document[records.Requirement] {
	f := newRequirementFixture()
	for _, opt := range opts {
		opt(f)
	}
	document := layout.Document[records.Requirement]{
		Path:   f.path,
		Record: f.req,
	}
	if !f.nilFields {
		document.Fields = requirementFields(f.req)
	}
	return document
}

// ValidInterfaceDocument returns an interface document that satisfies
// every currently registered specification check when validated alone.
// Requirement options must not be passed to this builder.
func ValidInterfaceDocument(opts ...Option) layout.Document[records.InterfaceRecord] {
	f := newInterfaceFixture()
	for _, opt := range opts {
		opt(f)
	}
	document := layout.Document[records.InterfaceRecord]{
		Path:   f.path,
		Record: f.iface,
	}
	if !f.nilFields {
		document.Fields = interfaceFields()
	}
	return document
}

// ValidRequirementYAML returns YAML for a baseline-valid requirement.
// Interface options must not be passed to this builder.
func ValidRequirementYAML(opts ...Option) string {
	f := newRequirementFixture()
	for _, opt := range opts {
		opt(f)
	}
	return mustYAML(f.req)
}

// ValidInterfaceYAML returns YAML for a baseline-valid interface.
// Requirement options must not be passed to this builder.
func ValidInterfaceYAML(opts ...Option) string {
	f := newInterfaceFixture()
	for _, opt := range opts {
		opt(f)
	}
	return mustYAML(f.iface)
}

// WithID sets the requirement id and, unless WithPath was already
// applied, the canonical spec/requirements/<id>.yaml path.
func WithID(id string) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithID is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.ID = id
		if !f.pathSet && !f.isInterface {
			f.path = path.Join(layout.RequirementsDir, id+".yaml")
		}
	}
}

// WithPath sets the document path without changing the in-record id or name.
func WithPath(relPath string) Option {
	return func(f *fixture) {
		f.path = relPath
		f.pathSet = true
	}
}

// WithType sets the requirement EARS type.
func WithType(earsType string) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithType is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.Type = earsType
	}
}

// WithText sets the requirement text.
func WithText(text string) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithText is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.Text = text
	}
}

// WithStatus sets the requirement status.
func WithStatus(status string) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithStatus is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.Status = status
	}
}

// WithAppliesTo replaces applies_to.
func WithAppliesTo(applies records.Applicability) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithAppliesTo is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.AppliesTo = records.Applicability{
			Interfaces: copyStrings(applies.Interfaces),
			Scopes:     copyStrings(applies.Scopes),
		}
	}
}

// WithRelationships replaces relationships. Passing no arguments yields
// a non-nil empty slice, which remains schema-valid.
func WithRelationships(rels ...records.Relationship) Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithRelationships is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.req.Relationships = copyRelationships(rels)
	}
}

// WithoutFields leaves Fields nil so schema presence checks are skipped.
func WithoutFields() Option {
	return func(f *fixture) {
		if f.isInterface {
			panic("testutil: WithoutFields is a requirement option; do not pass it to ValidInterfaceDocument")
		}
		f.nilFields = true
	}
}

// WithName sets the interface name and, unless WithPath was already
// applied, the canonical spec/interfaces/<name>.yaml path.
func WithName(name string) Option {
	return func(f *fixture) {
		if !f.isInterface {
			panic("testutil: WithName is an interface option; do not pass it to ValidRequirementDocument")
		}
		f.iface.Name = name
		if !f.pathSet && f.isInterface {
			f.path = path.Join(layout.InterfacesDir, name+".yaml")
		}
	}
}

// WithKind sets the interface kind.
func WithKind(kind string) Option {
	return func(f *fixture) {
		if !f.isInterface {
			panic("testutil: WithKind is an interface option; do not pass it to ValidRequirementDocument")
		}
		f.iface.Kind = kind
	}
}

// WithDescription sets the interface description.
func WithDescription(description string) Option {
	return func(f *fixture) {
		if !f.isInterface {
			panic("testutil: WithDescription is an interface option; do not pass it to ValidRequirementDocument")
		}
		f.iface.Description = description
	}
}

func newRequirementFixture() *fixture {
	return &fixture{
		path: path.Join(layout.RequirementsDir, defaultRequirementID+".yaml"),
		req: records.Requirement{
			ID:   defaultRequirementID,
			Type: records.TypeEventDriven,
			Text: defaultRequirementText,
			AppliesTo: records.Applicability{
				Scopes: []string{defaultScope},
			},
			Relationships: []records.Relationship{},
			Status:        records.StatusActive,
		},
	}
}

func newInterfaceFixture() *fixture {
	return &fixture{
		path: path.Join(layout.InterfacesDir, defaultInterfaceName+".yaml"),
		iface: records.InterfaceRecord{
			Name:        defaultInterfaceName,
			Kind:        defaultInterfaceKind,
			Description: defaultInterfaceDescription,
		},
		isInterface: true,
	}
}

func requirementFields(req records.Requirement) map[string]bool {
	fields := map[string]bool{
		"id":         true,
		"type":       true,
		"text":       true,
		"applies_to": true,
		// The presence map records the keys a canonical document would carry,
		// including empty selector lists, so synthetic fixtures match a real
		// YAML parse.
		"applies_to.interfaces": true,
		"applies_to.scopes":     true,
		"relationships":         true,
		"status":                true,
	}
	for i := range req.AppliesTo.Interfaces {
		fields[fmt.Sprintf("applies_to.interfaces[%d]", i)] = true
	}
	for i := range req.AppliesTo.Scopes {
		fields[fmt.Sprintf("applies_to.scopes[%d]", i)] = true
	}
	for i := range req.Relationships {
		fields[fmt.Sprintf("relationships[%d]", i)] = true
		fields[fmt.Sprintf("relationships[%d].type", i)] = true
		fields[fmt.Sprintf("relationships[%d].target", i)] = true
	}
	return fields
}

func interfaceFields() map[string]bool {
	return map[string]bool{
		"name":        true,
		"kind":        true,
		"description": true,
	}
}

func mustYAML(value any) string {
	var buf strings.Builder
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		panic(fmt.Sprintf("marshal test fixture YAML: %v", err))
	}
	if err := encoder.Close(); err != nil {
		panic(fmt.Sprintf("close test fixture YAML encoder: %v", err))
	}
	return buf.String()
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyRelationships(in []records.Relationship) []records.Relationship {
	out := make([]records.Relationship, len(in))
	copy(out, in)
	return out
}
