package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// LabelRequest and LabelWorkItem are filter-only GitHub labels. Lifecycle
// authority lives in the ProtoBot JSON body, never in label names.
const (
	LabelRequest  = "protobot:request"
	LabelWorkItem = "protobot:work-item"
)

// ErrNotFound indicates the GitHub issue does not exist.
var ErrNotFound = errors.New("github issue not found")

// ErrTransient indicates a retryable GitHub API failure.
var ErrTransient = errors.New("transient github api error")

// Issue is the minimal GitHub issue representation used by the translator.
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
	State  string // "open" or "closed"
}

// CreateIssueInput is the create payload for GitHub Issues.
type CreateIssueInput struct {
	Title  string
	Body   string
	Labels []string
}

// UpdateIssueInput is the update payload for GitHub Issues.
type UpdateIssueInput struct {
	Title  *string
	Body   *string
	Labels []string
	State  *string
}

// ListIssuesFilter selects issues for ProtoBot resource scans.
type ListIssuesFilter struct {
	Labels []string
	State  string // empty means all
}

// Client is the GitHub Issues boundary. Tests inject a fake; a later REST
// binding may implement the same interface over HTTP.
type Client interface {
	CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error)
	GetIssue(ctx context.Context, number int) (Issue, error)
	UpdateIssue(ctx context.Context, number int, input UpdateIssueInput) (Issue, error)
	ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error)
}

// TransientStatus reports whether an HTTP status should be retried.
func TransientStatus(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

// RetryingClient wraps Client with bounded retries on transient failures.
type RetryingClient struct {
	Inner       Client
	MaxAttempts int
	Backoff     time.Duration
	Sleep       func(time.Duration)
}

// CreateIssue retries transient failures.
func (c *RetryingClient) CreateIssue(ctx context.Context, input CreateIssueInput) (Issue, error) {
	return retryCall(c, func() (Issue, error) { return c.Inner.CreateIssue(ctx, input) })
}

// GetIssue retries transient failures.
func (c *RetryingClient) GetIssue(ctx context.Context, number int) (Issue, error) {
	return retryCall(c, func() (Issue, error) { return c.Inner.GetIssue(ctx, number) })
}

// UpdateIssue retries transient failures. An exhausted ambiguous failure
// surfaces as ErrTransient so the adapter can emit UNKNOWN_MUTATION.
func (c *RetryingClient) UpdateIssue(ctx context.Context, number int, input UpdateIssueInput) (Issue, error) {
	return retryCall(c, func() (Issue, error) { return c.Inner.UpdateIssue(ctx, number, input) })
}

// ListIssues retries transient failures.
func (c *RetryingClient) ListIssues(ctx context.Context, filter ListIssuesFilter) ([]Issue, error) {
	var last error
	attempts := c.attempts()
	for i := 0; i < attempts; i++ {
		issues, err := c.Inner.ListIssues(ctx, filter)
		if err == nil {
			return issues, nil
		}
		if !errors.Is(err, ErrTransient) {
			return nil, err
		}
		last = err
		if i+1 < attempts {
			c.sleep()
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrTransient, last)
}

func retryCall(c *RetryingClient, call func() (Issue, error)) (Issue, error) {
	var last error
	attempts := c.attempts()
	for i := 0; i < attempts; i++ {
		issue, err := call()
		if err == nil {
			return issue, nil
		}
		if !errors.Is(err, ErrTransient) {
			return Issue{}, err
		}
		last = err
		if i+1 < attempts {
			c.sleep()
		}
	}
	return Issue{}, fmt.Errorf("%w: %v", ErrTransient, last)
}

func (c *RetryingClient) attempts() int {
	if c.MaxAttempts <= 0 {
		return 3
	}
	return c.MaxAttempts
}

func (c *RetryingClient) sleep() {
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Millisecond
	}
	if c.Sleep != nil {
		c.Sleep(backoff)
		return
	}
	time.Sleep(backoff)
}
