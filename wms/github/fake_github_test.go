package github

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// FakeClient is an in-memory GitHub Issues API used by adapter tests.
type FakeClient struct {
	mu                        sync.Mutex
	nextNumber                int
	issues                    map[int]Issue
	transientLeft             map[string]int // op -> remaining transient failures
	callCounts                map[string]int
	createSucceedThenFailLeft int // create stores the issue, then returns ErrTransient
}

// NewFakeClient constructs an empty fake GitHub Issues API.
func NewFakeClient() *FakeClient {
	return &FakeClient{
		nextNumber:    1,
		issues:        make(map[int]Issue),
		transientLeft: make(map[string]int),
		callCounts:    make(map[string]int),
	}
}

// InjectTransient queues n transient failures for the next calls of op
// ("create", "get", "update", "list").
func (f *FakeClient) InjectTransient(op string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transientLeft[op] = n
}

// InjectCreateSucceedThenTransient queues n CreateIssue calls that persist the
// issue and then return ErrTransient (lost response after apply).
func (f *FakeClient) InjectCreateSucceedThenTransient(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createSucceedThenFailLeft = n
}

// CallCount returns how many times op was invoked.
func (f *FakeClient) CallCount(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCounts[op]
}

// IssueCount returns the number of issues created.
func (f *FakeClient) IssueCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.issues)
}

// GetIssueRaw returns the stored issue without counting calls (test helper).
func (f *FakeClient) GetIssueRaw(number int) (Issue, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[number]
	return cloneIssue(issue), ok
}

func (f *FakeClient) CreateIssue(_ context.Context, input CreateIssueInput) (Issue, error) {
	f.mu.Lock()
	f.callCounts["create"]++
	if f.transientLeft["create"] > 0 {
		f.transientLeft["create"]--
		f.mu.Unlock()
		return Issue{}, fmt.Errorf("%w: injected 429", ErrTransient)
	}
	number := f.nextNumber
	f.nextNumber++
	issue := Issue{
		Number: number,
		Title:  input.Title,
		Body:   input.Body,
		Labels: append([]string(nil), input.Labels...),
		State:  "open",
	}
	f.issues[number] = issue
	lostResponse := f.createSucceedThenFailLeft > 0
	if lostResponse {
		f.createSucceedThenFailLeft--
	}
	f.mu.Unlock()
	if lostResponse {
		return Issue{}, fmt.Errorf("%w: lost response after create", ErrTransient)
	}
	return cloneIssue(issue), nil
}

func (f *FakeClient) GetIssue(_ context.Context, number int) (Issue, error) {
	if err := f.bump("get"); err != nil {
		return Issue{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[number]
	if !ok {
		return Issue{}, ErrNotFound
	}
	return cloneIssue(issue), nil
}

func (f *FakeClient) UpdateIssue(_ context.Context, number int, input UpdateIssueInput) (Issue, error) {
	if err := f.bump("update"); err != nil {
		return Issue{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[number]
	if !ok {
		return Issue{}, ErrNotFound
	}
	if input.Title != nil {
		issue.Title = *input.Title
	}
	if input.Body != nil {
		issue.Body = *input.Body
	}
	if input.Labels != nil {
		issue.Labels = append([]string(nil), input.Labels...)
	}
	if input.State != nil {
		issue.State = *input.State
	}
	f.issues[number] = issue
	return cloneIssue(issue), nil
}

func (f *FakeClient) ListIssues(_ context.Context, filter ListIssuesFilter) ([]Issue, error) {
	if err := f.bump("list"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Issue, 0)
	for _, issue := range f.issues {
		if filter.State != "" && issue.State != filter.State {
			continue
		}
		if len(filter.Labels) > 0 && !containsAll(issue.Labels, filter.Labels) {
			continue
		}
		out = append(out, cloneIssue(issue))
	}
	return out, nil
}

func (f *FakeClient) bump(op string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCounts[op]++
	if f.transientLeft[op] > 0 {
		f.transientLeft[op]--
		return fmt.Errorf("%w: injected 429", ErrTransient)
	}
	return nil
}

func cloneIssue(issue Issue) Issue {
	copied := issue
	copied.Labels = append([]string(nil), issue.Labels...)
	return copied
}

func containsAll(have, need []string) bool {
	for _, label := range need {
		if !slices.Contains(have, label) {
			return false
		}
	}
	return true
}
