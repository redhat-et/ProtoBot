package scm

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/redhat-et/protobot/source-control-manager/internal/gitx"
	"github.com/redhat-et/protobot/source-control-manager/internal/host"
	"github.com/redhat-et/protobot/source-control-manager/internal/jsonx"
	"github.com/redhat-et/protobot/source-control-manager/internal/project"
	"github.com/redhat-et/protobot/source-control-manager/internal/render"
	"github.com/redhat-et/protobot/source-control-manager/internal/result"
)

// Pull-request actions of publish.
const (
	actionCreated   = "created"
	actionUpdated   = "updated"
	actionUnchanged = "unchanged"
)

// hostFailure maps a failed host request before any push.
func hostFailure(herr *host.Error) *result.Failure {
	switch hostLookupCode(herr) {
	case result.HostUnavailable:
		return result.Fail(result.HostUnavailable, "The host cannot be reached, so the pull request's state is unknown.")
	case result.CredentialUnavailable:
		return result.Fail(result.CredentialUnavailable, "The credential for the host is missing or expired.",
			jsonx.F("credential_source", credentialSource))
	}
	return result.Fail(result.HostRequestFailed, "The host refused or failed the lookup of the pull request.",
		jsonx.F("class", herr.Class))
}

// publish pushes the change-set branch and creates or updates its pull
// request.
func (c *call) publish() (*outcome, *result.Failure) {
	repo := c.config().Repository

	// Step 2: the working tree must equal the commit that is pushed.
	fs, failure := c.deriveFileSet("HEAD", listMode)
	if failure != nil {
		return nil, failure
	}
	if listed := fs.listed(); len(listed) > 0 {
		return nil, result.Fail(result.UncommittedChanges, "A path of the change set has an uncommitted change.",
			jsonx.F("paths", listed))
	}

	// Step 3.
	remote, failure := c.canonicalRemote()
	if failure != nil {
		return nil, failure
	}
	if failure := c.fetch(remote); failure != nil {
		return nil, failure
	}
	defaultHead, hasDefault, err := c.git.Commit("refs/remotes/" + remote + "/" + repo.DefaultBranch)
	if err != nil {
		return nil, c.gitFailure(err)
	}
	base := c.show.ChangeSet.BaseCommit

	// Step 4: no push runs while the pull request's state is unknown.
	adapter, ok := c.hostAdapter()
	if !ok {
		return nil, result.Fail(result.HostRequestFailed, "The canonical remote names no host repository.",
			jsonx.F("class", host.ClassOther))
	}
	pulls, herr := adapter.Find(c.branch, repo.DefaultBranch)
	if herr != nil {
		return nil, hostFailure(herr)
	}
	if len(pulls) > 1 {
		var numbers []int
		for _, p := range pulls {
			numbers = append(numbers, p.Number)
		}
		sort.Ints(numbers)
		return nil, result.Fail(result.AmbiguousPullRequest, "More than one pull request matches the branch.",
			jsonx.F("pull_requests", numbers))
	}
	var existing *host.PullRequest
	if len(pulls) == 1 {
		existing = &pulls[0]
		switch existing.State {
		case host.StateMerged:
			return nil, result.Fail(result.PRMerged, "The pull request is merged; register the change set instead.",
				jsonx.F("pull_request", existing.Number), jsonx.F("merge_commit", nullable(existing.MergeCommit)))
		case host.StateClosed:
			return nil, result.Fail(result.PRClosed, "The pull request is closed without a merge.",
				jsonx.F("pull_request", existing.Number))
		}
	}

	// Step 5: check the base.
	if !hasDefault {
		return nil, result.Fail(result.BaseNotOnDefault, "The remote has no default branch.",
			jsonx.F("base_commit", nullable(base)), jsonx.F("default_head", nil))
	}
	head, _, err := c.git.Commit("HEAD")
	if err != nil {
		return nil, c.gitFailure(err)
	}
	published, err := c.git.IsAncestor(head, defaultHead)
	if err != nil {
		return nil, c.gitFailure(err)
	}
	if published {
		return nil, result.Fail(result.NothingToPublish, "The branch has no commit that the default branch lacks.")
	}
	baseDetails := func(extra ...jsonx.Field) []jsonx.Field {
		return append([]jsonx.Field{jsonx.F("base_commit", nullable(base)), jsonx.F("default_head", defaultHead)}, extra...)
	}
	baseOnDefault := false
	if resolved, known, err := c.git.Commit(base); err != nil {
		return nil, c.gitFailure(err)
	} else if known && resolved == base {
		if baseOnDefault, err = c.git.IsAncestor(resolved, defaultHead); err != nil {
			return nil, c.gitFailure(err)
		}
	}
	if !baseOnDefault {
		return nil, result.Fail(result.BaseNotOnDefault, "base_commit is not on the canonical default branch.", baseDetails()...)
	}
	mergedIn, err := c.git.IsAncestor(defaultHead, head)
	if err != nil {
		return nil, c.gitFailure(err)
	}
	if !mergedIn {
		return nil, result.Fail(result.DefaultMoved, "The default branch moved since the change set's base.", baseDetails()...)
	}
	if defaultHead != base {
		return nil, result.Fail(result.BaseCommitStale,
			"The default branch was merged in, and base_commit was not updated. "+
				"After refresh, run repo_state before the suggested change-set update or check.",
			baseDetails(jsonx.F("next", []string{"ears-manager", "--output", "json", "change-set", "update",
				"--change-set", c.csID, "--base-commit", defaultHead}))...)
	}

	// Step 6: render the title and the body.
	intent := c.show.ChangeSet.Intent
	title := render.Title(intent)
	if render.ActsOnGitHub(intent) || render.ActsOnGitHub(title) {
		return nil, result.Fail(result.UnsafeText, "The intent holds text that GitHub acts on.", jsonx.F("field", "intent"))
	}
	// GitHub refuses an empty or a longer title, and a longer body, which
	// would fail only after the push; the same intent would fail again.
	if title == "" {
		return nil, result.Fail(result.UnsafeText, "The intent is empty, and a pull request needs a title.",
			jsonx.F("field", "intent"), jsonx.F("reason", "the title is empty"))
	}
	if utf8.RuneCountInString(title) > render.MaxTitleLength {
		return nil, result.Fail(result.UnsafeText, "The intent is longer than a pull-request title may be.",
			jsonx.F("field", "intent"), jsonx.F("reason", fmt.Sprintf("the title is longer than %d characters", render.MaxTitleLength)))
	}
	body, failure := c.renderBody(defaultHead, head)
	if failure != nil {
		return nil, failure
	}
	if utf8.RuneCountInString(body) > render.MaxBodyLength {
		return nil, result.Fail(result.UnsafeText, "The rendered pull-request body is longer than the host takes.",
			jsonx.F("field", "body"), jsonx.F("reason", fmt.Sprintf("the body is longer than %d characters", render.MaxBodyLength)))
	}

	// Step 7: push, without force and without tags.
	ref := "refs/heads/" + c.branch
	if failure := c.allowWrite(ref, true); failure != nil {
		return nil, failure
	}
	pushed, failure := c.push(remote, ref)
	if failure != nil {
		return nil, failure
	}

	// Step 8: create or update the pull request.
	var refs []string
	if pushed {
		refs = []string{ref}
	}
	// A failed create or update after the push: when the host refused it,
	// the host did not change, and only the push can have; when the host
	// may have applied it before the answer failed, the outcome is open.
	afterPush := func(herr *host.Error, number int) *result.Failure {
		f := result.Fail(result.HostRequestFailed, "The host failed the pull-request request after the push.",
			jsonx.F("class", herr.Class), jsonx.F("branch", c.branch), jsonx.F("pushed", head))
		if number > 0 {
			f.Details = append(f.Details, jsonx.F("pull_request", number))
		}
		if pushed {
			f.Details = append(f.Details, jsonx.F("refs", refs))
		}
		switch {
		case herr.Open():
			f.Message = "The host may have applied the pull-request request before it failed; read the state with repo_state."
			f = f.WithMutation(result.MutationUnknown).WithRetry(result.RetryReconcile)
		case pushed:
			f = f.WithMutation(result.MutationPartial)
		}
		return f
	}
	action := actionUnchanged
	var pr host.PullRequest
	switch {
	case existing == nil:
		created, herr := adapter.Create(repo.DefaultBranch, c.branch, title, body)
		if herr != nil {
			return nil, afterPush(herr, 0)
		}
		pr, action = created, actionCreated
	case existing.Title != title || existing.Body != body:
		if herr := adapter.Update(existing.Number, title, body); herr != nil {
			return nil, afterPush(herr, existing.Number)
		}
		pr, action = *existing, actionUpdated
	default:
		pr = *existing
	}

	applied := pushed || action != actionUnchanged
	status := result.OutcomeUnchanged
	if applied {
		status = result.OutcomeApplied
	}
	data := jsonx.O(
		jsonx.F("pushed", head),
		jsonx.F("pull_request", jsonx.O(jsonx.F("number", pr.Number), jsonx.F("action", action), jsonx.F("url", nullable(pr.URL)))),
	)
	return &outcome{outcome: status, data: data, mutation: result.Mutation{Applied: applied, Refs: refs, Remote: applied}}, nil
}

// renderBody reads compare and impact, and the files from the merge base of
// the default head and HEAD, and renders the pull-request body.
func (c *call) renderBody(defaultHead, head string) (string, *result.Failure) {
	compare, failure := c.em.CompareChangeSet(c.csID)
	if failure != nil {
		return "", failure
	}
	impact, failure := c.em.ImpactOf(c.csID)
	if failure != nil {
		return "", failure
	}
	mergeBase, err := c.git.Read("merge-base", defaultHead, head)
	if err != nil {
		return "", c.gitFailure(err)
	}
	out, err := c.git.ReadRaw(gitx.Opts{}, "diff-tree", "-r", "--name-only", "--no-commit-id", "--no-renames", "-z", mergeBase, head)
	if err != nil {
		return "", c.gitFailure(err)
	}
	files := gitx.SplitZ(out)
	sort.Strings(files)
	return render.Body(render.BodyInput{
		ChangeSetID: c.csID,
		Manifest:    c.show.ChangeSet,
		Compare:     compare,
		Impact:      impact,
		Files:       files,
	}), nil
}

// push pushes ref to the same name on remote and reports whether the
// remote changed.
func (c *call) push(remote, ref string) (bool, *result.Failure) {
	args := []string{"push", "--porcelain", "--no-follow-tags", remote, ref + ":" + ref}
	res, err := c.git.Run(gitx.Opts{Record: true}, args...)
	if err != nil {
		return false, c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	flag, summary, found := pushStatus(string(res.Stdout), ref)
	if res.OK() && found {
		return flag != '=', nil
	}
	if found && flag == '!' {
		branch := strings.TrimPrefix(ref, "refs/heads/")
		if strings.Contains(summary, "[remote rejected]") {
			if strings.Contains(strings.ToLower(summary), "protected") {
				return false, result.Fail(result.PushRejectedProtected, "The host protects the change-set branch.",
					jsonx.F("branch", branch))
			}
			// A rule, a secret scan, or a server hook refused the push. The
			// reason is the host's text, which the result never carries.
			return false, result.Fail(result.GitFailed, "The remote rejected the push; run the push in your own shell to see why.",
				jsonx.F("branch", branch), jsonx.F("command", append([]string{"git"}, args...))).WithRetry(result.RetryUser)
		}
		remoteHead, _, _ := c.git.Commit("refs/remotes/" + remote + "/" + branch)
		return false, result.Fail(result.PushRejectedNonFastForward, "The remote branch has commits that HEAD lacks.",
			jsonx.F("branch", branch), jsonx.F("remote_head", nullable(remoteHead)))
	}
	if code := transportClass(string(res.Stderr)); code != "" {
		return false, pushTransportFailure(code, c.transportFailure(remote, args, res))
	}
	return false, commandFailure(args, res).WithMutation(result.MutationUnknown)
}

// pushTransportFailure marks a push that failed in transport. The
// connection can fail after the remote updated the ref, so the outcome is
// open. Git authenticates before it sends the pack, so a missing
// credential changed nothing.
func pushTransportFailure(code result.Code, f *result.Failure) *result.Failure {
	if code == result.RemoteUnavailable {
		return f.WithMutation(result.MutationUnknown).WithRetry(result.RetryReconcile)
	}
	return f
}

// pushStatus finds the porcelain status line of ref: its flag and summary.
func pushStatus(out, ref string) (byte, string, bool) {
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 || len(parts[0]) != 1 {
			continue
		}
		if parts[1] == ref+":"+ref {
			return parts[0][0], parts[2], true
		}
	}
	return 0, "", false
}

// refresh merges <remote>/<default> into the change-set branch with a merge
// commit, and aborts on a conflict.
func (c *call) refresh() (*outcome, *result.Failure) {
	repo := c.config().Repository

	// Step 2.
	changes, err := c.git.TrackedChanges()
	if err != nil {
		return nil, c.gitFailure(err)
	}
	if len(changes) > 0 {
		return nil, uncommitted(changes)
	}
	// Step 3.
	remote, failure := c.canonicalRemote()
	if failure != nil {
		return nil, failure
	}
	if failure := c.fetch(remote); failure != nil {
		return nil, failure
	}
	from := remote + "/" + repo.DefaultBranch
	defaultHead, ok, err := c.git.Commit("refs/remotes/" + from)
	if err != nil {
		return nil, c.gitFailure(err)
	}
	if !ok {
		return nil, result.Fail(result.GitFailed, "The remote has no default branch.", jsonx.F("from", from))
	}
	head, _, err := c.git.Commit("HEAD")
	if err != nil {
		return nil, c.gitFailure(err)
	}
	base := c.show.ChangeSet.BaseCommit
	update := func() jsonx.Obj {
		if base == defaultHead {
			return jsonx.O(jsonx.F("required", false), jsonx.F("value", nil))
		}
		return jsonx.O(jsonx.F("required", true), jsonx.F("value", defaultHead))
	}

	// Step 4.
	current, err := c.git.IsAncestor(defaultHead, head)
	if err != nil {
		return nil, c.gitFailure(err)
	}
	if current {
		data := jsonx.O(jsonx.F("merge_commit", nil), jsonx.F("from", from), jsonx.F("base_commit_update", update()))
		return &outcome{outcome: result.OutcomeUnchanged, data: data}, nil
	}
	message := render.RefreshMessage(from, c.branch, c.csID)
	if render.ActsOnGitHub(message) {
		// Only a remote or branch name that the role does not set can put
		// such text in the message, so the user renames it.
		return nil, result.Fail(result.UnsafeText, "The merge message holds text that GitHub acts on.",
			jsonx.F("field", "branch"), jsonx.F("remote", remote), jsonx.F("branch", c.branch)).WithRetry(result.RetryUser)
	}
	// A tag or a local branch named <remote>/<default> would win git's
	// lookup of the short name and merge a commit no one reviewed.
	if ok, failure := c.unambiguous(from, "refs/remotes/"+from); failure != nil {
		return nil, failure
	} else if !ok {
		return nil, ambiguousName(from, "refs/remotes/"+from)
	}
	ref := "refs/heads/" + c.branch
	if failure := c.allowWrite(ref, false); failure != nil {
		return nil, failure
	}
	args := []string{"merge", "--no-ff", "--cleanup=verbatim", "-m", message, from}
	res, err := c.git.Run(gitx.Opts{Record: true}, args...)
	if err != nil {
		return nil, c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	if !res.OK() {
		return nil, c.mergeFailure(args, res, head, from)
	}
	// The merge moved the branch, so a failed read from here on cannot
	// tell to which commit.
	merge, _, err := c.git.Commit("HEAD")
	if err != nil {
		return nil, c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	parents, err := c.git.Read("rev-list", "--parents", "-n", "1", merge)
	if err != nil {
		return nil, c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	if fields := strings.Fields(parents); len(fields) != 3 || fields[1] != head || fields[2] != defaultHead {
		return nil, result.Fail(result.GitFailed, "The merge commit does not join HEAD and the default head that the SCM checked.",
			jsonx.F("merge_commit", merge)).WithMutation(result.MutationUnknown)
	}
	data := jsonx.O(jsonx.F("merge_commit", merge), jsonx.F("from", from), jsonx.F("base_commit_update", update()))
	return &outcome{outcome: result.OutcomeApplied, data: data, mutation: result.Mutation{Applied: true, Refs: []string{ref}}}, nil
}

// mergeFailure is step 5 of refresh: on a conflict, abort the merge, so
// the working tree, the index, and HEAD are as they were.
func (c *call) mergeFailure(args []string, res gitx.Result, head, from string) *result.Failure {
	_, merging, err := c.git.Commit("MERGE_HEAD")
	if err != nil {
		return c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	if !merging {
		if files, ok := overwritten(res.Stderr); ok {
			return uncommitted(files)
		}
		if now, _, _ := c.git.Commit("HEAD"); now == head {
			return commandFailure(args, res)
		}
		return commandFailure(args, res).WithMutation(result.MutationUnknown)
	}
	out, err := c.git.ReadRaw(gitx.Opts{}, "ls-files", "-u", "-z")
	if err != nil {
		return c.gitFailure(err).WithMutation(result.MutationUnknown)
	}
	seen := map[string]bool{}
	var conflicted []string
	for _, line := range gitx.SplitZ(out) {
		if _, p, ok := strings.Cut(line, "\t"); ok && !seen[p] {
			seen[p] = true
			conflicted = append(conflicted, p)
		}
	}
	sort.Strings(conflicted)
	abortArgs := []string{"merge", "--abort"}
	abort, err := c.git.Run(gitx.Opts{Record: true}, abortArgs...)
	if err != nil || !abort.OK() {
		return result.Fail(result.GitFailed, "The conflicted merge could not be aborted.",
			jsonx.F("command", append([]string{"git"}, abortArgs...))).WithMutation(result.MutationUnknown)
	}
	if now, _, _ := c.git.Commit("HEAD"); now != head {
		return result.Fail(result.GitFailed, "HEAD moved during the aborted merge.").WithMutation(result.MutationUnknown)
	}
	files := []jsonx.Obj{}
	for _, p := range conflicted {
		files = append(files, jsonx.O(jsonx.F("path", p), jsonx.F("class", c.conflictClass(p))))
	}
	return result.Fail(result.MergeConflict, "The merge conflicted and was aborted.",
		jsonx.F("from", from), jsonx.F("files", files))
}

// Conflict classes, after #34's failure table.
const (
	classManifest    = "change-set-manifest"
	classIndex       = "index-file"
	classRequirement = "requirement-record"
	classArtifact    = "registered-artifact"
	classOther       = "other"
)

func (c *call) conflictClass(p string) string {
	stores := c.config().Stores
	switch {
	case project.Under(p, stores.ChangeSets):
		return classManifest
	case p == project.ConfigPath || p == project.ProjectionPath:
		return classIndex
	case project.Under(p, stores.Requirements):
		return classRequirement
	case project.Under(p, stores.Interfaces):
		return classArtifact
	}
	if _, ok := c.config().ArtifactFor(p); ok {
		return classArtifact
	}
	return classOther
}
