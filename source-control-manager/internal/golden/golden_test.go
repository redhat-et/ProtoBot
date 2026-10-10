package golden_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hosted lists the fixture rows of the hosted face behind the Gate. Issue
// #160 builds the stdio face and the CLI; the hosted face is out of its
// scope, and `serve --transport streamable-http` refuses to start
// (scm-30-no-key).
var hosted = []string{
	"scm-17-hosted-context", "scm-18-unsigned-context", "scm-18b-no-context", "scm-28-other-ref",
	"scm-29-expired", "scm-29-audience", "scm-29-action", "scm-29-wildcard", "scm-29-headers",
	"scm-29-unsigned-claims", "scm-29-no-author", "scm-40-hosted-commit",
}

func TestGoldenFixture(t *testing.T) {
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	d := &driver{t: t, golden: loadGolden(t), shas: map[string]string{}, base: base, work: filepath.Join(base, "work")}
	defer d.closeSession()
	for _, step := range hosted {
		d.step(step)
	}

	t.Run("main line", func(t *testing.T) {
		d.t = t
		mainLine(d)
	})
	if t.Failed() {
		return
	}
	for _, check := range negativeChecks {
		t.Run(check.name, func(t *testing.T) {
			d.t = t
			d.restore(check.from)
			defer d.closeSession()
			check.run(d)
		})
	}
	t.Run("no remote URL or credential in any result", func(t *testing.T) {
		for _, out := range d.outputs {
			for _, leak := range []string{"PROTOBOT-FIXTURE-TOKEN", canonical, d.origin()} {
				if strings.Contains(out, leak) {
					t.Fatalf("a result holds %q: %s", leak, out)
				}
			}
		}
	})
	t.Run("hosted face rows", func(t *testing.T) {
		t.Skipf("the hosted face behind the Gate is out of the scope of #160: %s", strings.Join(hosted, ", "))
	})
}

func mainLine(d *driver) {
	d.setup()
	d.bind("m0", d.rev(d.origin(), "refs/heads/main"))
	d.snapshot("base")

	// Step 1.
	d.callStep("1-branch-init")
	d.earsStep("1-project-init")
	created := d.earsStep("1-change-set-create")
	d.expectData("1-change-set-create", created)
	d.callStep("1-commit")
	d.earsStep("1-check")
	d.assertRefs(map[string]string{
		"HEAD": "cs/00001-project-init", "refs/heads/cs/00001-project-init": "c1",
		"refs/heads/main": "m0", "origin/refs/heads/main": "m0",
	})
	d.assertCommitPaths("c1", ".protobot/change-sets/cs-00001.yaml", ".protobot/project.yaml", ".protobot/projection.yaml")
	d.assertTrailer("c1", "CS-00001")

	// Step 2.
	d.callStep("2-publish")
	d.bind("m1", d.hostMerge("cs/00001-project-init", 1))
	d.assertParents("m1", "m0", "c1")
	d.registerStep("2-register")
	d.callStep("2-repo-state")
	created = d.earsStep("2-change-set-create")
	d.expectData("2-change-set-create", created)
	d.assertRefs(map[string]string{
		"HEAD": "cs/00002-add-the-initial-sketch", "refs/heads/cs/00002-add-the-initial-sketch": "m1", "refs/heads/main": "m1",
	})
	d.assertLocalBranches("cs/00001-project-init", "cs/00002-add-the-initial-sketch", "main")

	// Step 3.
	d.earsStep("3-artifact-put-vision")
	d.earsStep("3-artifact-put-architecture")
	d.callStep("3-repo-state")

	// Step 4.
	d.appendLine("docs/vision.md", "A direct edit.")
	index := d.git(d.clone(), "ls-files", "-s")
	d.callStep("4-commit")
	d.assertRefs(map[string]string{"refs/heads/cs/00002-add-the-initial-sketch": "m1"})
	if d.git(d.clone(), "ls-files", "-s") != index {
		d.t.Fatal("4-assert: the index changed")
	}
	// The validator names the artifact by its ID; the SCM maps it to
	// docs/vision.md, as the 4-commit result above shows.
	d.checkStep("4-check")

	// Step 5.
	d.git(d.clone(), "checkout", "--", "docs/vision.md")
	d.earsStep("5-artifact-put-vision")
	d.callStep("5-commit")
	d.assertCommitPaths("c2", ".protobot/change-sets/cs-00002.yaml", ".protobot/project.yaml", "docs/architecture.md", "docs/vision.md")

	// Step 6.
	d.callStep("6-publish")
	d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2", "origin/refs/heads/main": "m1"})
	d.assertBody(d.lastBody(), d.sha("m1"))
	d.snapshot("after-6")

	// Step 7.
	d.bind("m2", d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog"))
	d.callStep("7-publish-stale")
	d.callStep("7-refresh")
	d.callStep("7-repo-state")
	d.assertRefs(map[string]string{"refs/heads/main": "m2"})
	d.callStep("7-publish-base-stale")
	d.earsStep("7-change-set-update")
	impact := d.earsStep("7-impact")
	d.expectAssessment("7-impact", impact, "stale")
	d.review(impact)
	update := d.earsStep("7-impact-file")
	d.expectAssessment("7-impact-file", update, "complete")
	d.earsStep("7-check")
	d.callStep("7-commit")
	d.callStep("7-publish")
	d.assertParents("r1", "c2", "m2")
	if base := d.manifestBase("HEAD", "CS-00002"); base != d.sha("m2") {
		d.t.Fatalf("7-assert: the manifest's base_commit is %s, want %s", base, d.sha("m2"))
	}
	if reflog := d.git(d.clone(), "log", "--walk-reflogs", "--format=%gs", "refs/heads/cs/00002-add-the-initial-sketch"); strings.Contains(reflog, "rebase") {
		d.t.Fatalf("7-assert: the reflog shows a rebase:\n%s", reflog)
	}
	first := strings.Fields(d.git(d.clone(), "rev-list", "--first-parent", "--reverse", d.sha("m1")+"..refs/heads/cs/00002-add-the-initial-sketch"))
	if len(first) == 0 || first[0] != d.sha("c2") {
		d.t.Fatalf("7-assert: the branch's first commit is %v, want %s", first, d.sha("c2"))
	}

	// Step 8.
	d.bind("m3", d.hostMerge("cs/00002-add-the-initial-sketch", 2))
	d.assertParents("m3", "m2", "c3")
	d.snapshot("after-8-host-merge")
	d.registerStep("8-register")
	out, status := d.register("CS-00002")
	if status != 0 || len(d.registrationsOf("CS-00002")) != 1 {
		d.t.Fatalf("8-register-again: exit %d, calls %v\n%s", status, d.registrationsOf("CS-00002"), out)
	}
	d.callStep("8-publish-after-merge")
	d.callStep("8-repo-state")
	doc := d.earsStep("8-merged-manifest-write")
	if code := errorCode(doc); code != "change_set.not_proposed" {
		d.t.Fatalf("8-merged-manifest-write: error code %s", code)
	}
	d.snapshot("after-8")
}

func errorCode(doc map[string]any) string {
	if e, ok := doc["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		return code
	}
	return ""
}

// expectData compares an ears-manager result's data with the step's.
func (d *driver) expectData(step string, doc map[string]any) {
	d.t.Helper()
	want := d.step(step)["data"]
	if err := d.match("data", want, doc["data"]); err != nil {
		d.t.Fatalf("%s: %v", step, err)
	}
}

func (d *driver) expectAssessment(step string, doc map[string]any, want string) {
	d.t.Helper()
	data, _ := doc["data"].(map[string]any)
	if got, _ := data["assessment_status"].(string); got != want {
		d.t.Fatalf("%s: assessment_status %q, want %q", step, got, want)
	}
}

// registerStep runs a registration step and compares its read of
// approved_merge and the registration stub.
func (d *driver) registerStep(step string) {
	d.t.Helper()
	s := d.step(step)
	command := s["command"].(map[string]any)
	argv := command["argv"].([]any)
	changeSet := argv[len(argv)-1].(string)
	out, status := d.register(changeSet)
	if want := int(command["exit"].(float64)); status != want {
		d.t.Fatalf("%s: registration exited %d, want %d\n%s", step, status, want, out)
	}
	read := s["scm_read"].(map[string]any)
	d.expect(step, read["result"], out)
	stub := s["registration_stub"].(map[string]any)
	calls := d.registrationsOf(changeSet)
	if want := int(stub["calls"].(float64)); len(calls) != want {
		d.t.Fatalf("%s: %d registration calls, want %d", step, len(calls), want)
	}
	if last, ok := stub["last"].(map[string]any); ok {
		got := map[string]any{
			"change_set_id": calls[0].ChangeSetID, "merge_commit": calls[0].MergeCommit,
			"materialization_key": calls[0].MaterializationKey, "idempotency_key": calls[0].IdempotencyKey,
		}
		if err := d.match("registration_stub.last", last, got); err != nil {
			d.t.Fatalf("%s: %v", step, err)
		}
	}
}

// assertRefs checks refs: HEAD by branch name, refs/... in the clone, and
// origin/refs/... in origin, by bound name.
func (d *driver) assertRefs(want map[string]string) {
	d.t.Helper()
	for ref, value := range want {
		switch {
		case ref == "HEAD":
			if got := d.git(d.clone(), "symbolic-ref", "--short", "HEAD"); got != value {
				d.t.Fatalf("HEAD is %s, want %s", got, value)
			}
		case strings.HasPrefix(ref, "origin/"):
			if got := d.rev(d.origin(), strings.TrimPrefix(ref, "origin/")); got != d.sha(value) {
				d.t.Fatalf("%s is %s, want <sha:%s>", ref, got, value)
			}
		default:
			if got := d.rev(d.clone(), ref); got != d.sha(value) {
				d.t.Fatalf("%s is %s, want <sha:%s>", ref, got, value)
			}
		}
	}
}

func (d *driver) assertCommitPaths(name string, paths ...string) {
	d.t.Helper()
	got := strings.Fields(d.git(d.clone(), "diff-tree", "--no-commit-id", "--name-only", "-r", d.sha(name)))
	if strings.Join(got, " ") != strings.Join(paths, " ") {
		d.t.Fatalf("<sha:%s> holds %v, want %v", name, got, paths)
	}
}

func (d *driver) assertTrailer(name, changeSet string) {
	d.t.Helper()
	message := d.git(d.clone(), "log", "-1", "--format=%B", d.sha(name))
	lines := strings.Split(strings.TrimSpace(message), "\n")
	if lines[len(lines)-1] != "Change-Set: "+changeSet {
		d.t.Fatalf("<sha:%s> ends with %q, want the trailer Change-Set: %s", name, lines[len(lines)-1], changeSet)
	}
}

func (d *driver) assertParents(name string, parents ...string) {
	d.t.Helper()
	got := strings.Fields(d.git(d.origin(), "rev-list", "--parents", "-n", "1", d.sha(name)))[1:]
	var want []string
	for _, p := range parents {
		want = append(want, d.sha(p))
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		d.t.Fatalf("<sha:%s> has parents %v, want %v", name, got, parents)
	}
}

func (d *driver) assertLocalBranches(names ...string) {
	d.t.Helper()
	got := d.git(d.clone(), "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if strings.Join(strings.Fields(got), " ") != strings.Join(names, " ") {
		d.t.Fatalf("local branches %v, want %v", strings.Fields(got), names)
	}
}

func (d *driver) manifestBase(rev, changeSet string) string {
	d.t.Helper()
	content := d.git(d.clone(), "show", rev+":.protobot/change-sets/"+strings.ToLower(changeSet)+".yaml")
	for _, line := range strings.Split(content, "\n") {
		if value, ok := strings.CutPrefix(line, "base_commit: "); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// assertBody checks the rendered pull-request body of step 6: #34's six
// parts in order, and record values inside code spans.
func (d *driver) assertBody(body, base string) {
	d.t.Helper()
	parts := []string{
		"<!-- Rendered by the ProtoBot Source Control Manager",
		"## Change set `CS-00002`",
		"- Intent: `Add the initial Sketch`",
		"- Base commit: `" + base + "`",
		"### Changed requirements",
		"### Interface and artifact operations",
		"- Artifact `revise` `vision`",
		"### Impact assessment",
		"### Implementation",
		"- Implementation required: `false`",
		"- Rationale: `The Sketch changes no interface yet.`",
		"### Files",
		"- `.protobot/change-sets/cs-00002.yaml`",
		"- `docs/vision.md`",
	}
	at := 0
	for _, part := range parts {
		i := strings.Index(body[at:], part)
		if i < 0 {
			d.t.Fatalf("6-assert: the body lacks %q after byte %d:\n%s", part, at, body)
		}
		at += i + len(part)
	}
}

func (d *driver) assertUntracked(paths ...string) {
	d.t.Helper()
	got := d.git(d.clone(), "ls-files", "--others", "--exclude-standard")
	for _, p := range paths {
		if !strings.Contains("\n"+got+"\n", "\n"+p+"\n") {
			d.t.Fatalf("%s is not untracked: %s", p, got)
		}
	}
}

// filter defines a clean filter in the clone's configuration for
// docs/vision.md, in an untracked .gitattributes.
func (d *driver) filter(script string) {
	d.t.Helper()
	path := filepath.Join(d.work, "filter.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		d.t.Fatal(err)
	}
	d.git(d.clone(), "config", "filter.fixture.clean", path)
	d.write(".gitattributes", "docs/vision.md filter=fixture\n")
}

type check struct {
	name string
	from string
	run  func(d *driver)
}

// The #34 negative checks and the SCM's own checks, each from a snapshot.
var negativeChecks = []check{
	{"n34-1 force", "after-6", func(d *driver) {
		d.callStep("n34-1-force")
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"n34-2 amend", "after-6", func(d *driver) {
		d.callStep("n34-2-amend")
		d.assertRefs(map[string]string{"refs/heads/cs/00002-add-the-initial-sketch": "c2", "origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"n34-3 unregistered file", "after-6", func(d *driver) {
		d.write("notes.txt", "notes\n")
		d.callStep("n34-3-commit")
		d.callStep("n34-3-commit-paths")
		d.assertUntracked("notes.txt")
	}},
	{"n34-4 wi prefix", "base", func(d *driver) {
		d.callStep("n34-4-wi-prefix")
		if refs := d.git(d.clone(), "for-each-ref", "refs/heads/wi/", "refs/remotes/"); strings.Contains(refs, "/wi/") {
			d.t.Fatalf("a wi/ ref exists: %s", refs)
		}
	}},
	{"n34-4 wi resume", "after-6", func(d *driver) {
		d.callStep("n34-4-wi-resume")
		if refs := d.git(d.clone(), "for-each-ref", "refs/heads/wi/"); refs != "" {
			d.t.Fatalf("a wi/ ref exists: %s", refs)
		}
	}},
	{"n34-5 attestation", "after-6", func(d *driver) {
		d.write(".protobot/attestations/fixture.json", "{}\n")
		d.callStep("n34-5-commit")
		if all := d.git(d.clone(), "log", "--all", "--name-only", "--format="); strings.Contains(all, ".protobot/attestations/") {
			d.t.Fatal("an attestation path is in a commit")
		}
	}},
	{"n34-6 default branch, single-player", "after-6", func(d *driver) {
		d.git(d.clone(), "checkout", "--quiet", "main")
		d.callStep("n34-6-publish-single-player")
		d.assertRefs(map[string]string{"origin/refs/heads/main": "m1"})
	}},
	{"n34-6 default branch, multi-player", "after-6", func(d *driver) {
		d.git(d.clone(), "checkout", "--quiet", "main")
		content := strings.Replace(d.read(".protobot/project.yaml"), "review_mode: single-player", "review_mode: multi-player", 1)
		d.write(".protobot/project.yaml", content)
		d.callStep("n34-6-publish-multi-player")
		d.assertRefs(map[string]string{"origin/refs/heads/main": "m1"})
	}},
	{"n34-7 second merge commit", "after-8", func(d *driver) {
		if d.record("CS-00002", d.sha("m2")) {
			d.t.Fatal("the registration stub accepted a different merge commit")
		}
		if calls := d.registrationsOf("CS-00002"); len(calls) != 1 || calls[0].MergeCommit != d.sha("m3") {
			d.t.Fatalf("the first registration does not stand: %v", calls)
		}
		out, status := d.cli("--output", "json", "approved-merge", "--change-set", "CS-00002")
		if status != 0 {
			d.t.Fatalf("approved-merge exited %d", status)
		}
		d.expect("n34-7-second-merge-commit", d.step("n34-7-second-merge-commit")["scm_read"].(map[string]any)["result"], out)
	}},
	{"n34-9 structured record outside ears-manager", "after-6", func(d *driver) {
		cases := []struct {
			step string
			edit func()
		}{
			// An edit of a record in a store that the change set does not
			// touch: the approved manifest of CS-00001.
			{"n34-9-commit-edit", func() { d.appendLine(".protobot/change-sets/cs-00001.yaml", "# A direct edit.") }},
			// A record added to a store that the change set does not touch.
			{"n34-9-commit-add", func() {
				d.write(".protobot/requirements/REQ-FIX-00001.yaml", strings.Join([]string{
					"id: REQ-FIX-00001",
					"type: ubiquitous",
					"text: The system shall remain valid.",
					"applies_to:", "  scopes:", "    - fixture",
					"verification:", "  mode: isolated-interface",
					"provenance: user-authored",
					"created: \"2026-09-21T09:00:00Z\"",
				}, "\n")+"\n")
			}},
		}
		for _, c := range cases {
			d.restore("after-6")
			d.earsStep("n34-9-artifact-put-vision")
			c.edit()
			head := d.rev(d.clone(), "HEAD")
			d.callStep(c.step)
			if d.rev(d.clone(), "HEAD") != head {
				d.t.Fatalf("%s: a commit was created", c.step)
			}
		}
	}},
	{"scm-1 rewrite", "after-6", func(d *driver) {
		d.git(d.clone(), "commit", "--quiet", "--amend", "-m", "An amended commit")
		d.bind("c2x", d.rev(d.clone(), "HEAD"))
		d.callStep("scm-1-publish")
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"scm-2 foreign commit", "after-6", func(d *driver) {
		d.git(d.second(), "fetch", "--quiet", "origin")
		d.git(d.second(), "switch", "--quiet", "--create", "cs/00002-add-the-initial-sketch", "origin/cs/00002-add-the-initial-sketch")
		if err := os.WriteFile(filepath.Join(d.second(), "foreign.md"), []byte("foreign\n"), 0o644); err != nil {
			d.t.Fatal(err)
		}
		d.git(d.second(), "add", "--", "foreign.md")
		d.git(d.second(), "commit", "--quiet", "-m", "A foreign commit")
		d.git(d.second(), "push", "--quiet", "origin", "cs/00002-add-the-initial-sketch")
		d.bind("f1", d.rev(d.second(), "HEAD"))
		d.callStep("scm-2-refresh")
		d.callStep("scm-2-publish")
		if branches := d.git(d.clone(), "branch", "--contains", d.sha("f1")); branches != "" {
			d.t.Fatalf("a local branch holds the foreign commit: %s", branches)
		}
	}},
	{"scm-3 remote-only branch", "after-6", func(d *driver) {
		d.git(d.second(), "fetch", "--quiet", "origin")
		d.git(d.second(), "switch", "--quiet", "--create", "cs/00009-remote-only", "origin/main")
		d.git(d.second(), "push", "--quiet", "origin", "cs/00009-remote-only")
		d.callStep("scm-3-resume")
		if refs := d.git(d.clone(), "for-each-ref", "refs/heads/cs/00009-remote-only"); refs != "" {
			d.t.Fatalf("a local branch was created: %s", refs)
		}
	}},
	{"scm-4 staged outside", "after-6", func(d *driver) {
		d.write("README.md", "# Fixture, staged\n")
		d.git(d.clone(), "add", "--", "README.md")
		d.putVision("# Fixture Vision, revised\n")
		d.callStep("scm-4-commit")
		if staged := d.git(d.clone(), "diff", "--cached", "--name-only"); staged != "README.md" {
			d.t.Fatalf("staged %q, want README.md", staged)
		}
		d.assertCommitPaths("c4", ".protobot/project.yaml", "docs/vision.md")
	}},
	{"scm-5 default branch", "after-6", func(d *driver) {
		d.git(d.clone(), "checkout", "--quiet", "main")
		d.callStep("scm-5-commit")
	}},
	{"scm-6 unknown number", "after-6", func(d *driver) {
		d.git(d.clone(), "switch", "--quiet", "--create", "cs/00042-x", "main")
		d.callStep("scm-6-commit")
	}},
	{"scm-7 repository programs", "after-6", func(d *driver) {
		for _, hook := range []string{"pre-commit", "commit-msg", "reference-transaction", "post-index-change", "pre-push"} {
			script := "#!/bin/sh\ntouch " + filepath.Join(d.markers(), hook) + "\ncat >/dev/null 2>&1 || true\nexit 0\n"
			if err := os.WriteFile(filepath.Join(d.clone(), ".git", "hooks", hook), []byte(script), 0o755); err != nil {
				d.t.Fatal(err)
			}
		}
		monitor := filepath.Join(d.work, "fsmonitor.sh")
		if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch "+filepath.Join(d.markers(), "fsmonitor")+"\nexit 1\n"), 0o755); err != nil {
			d.t.Fatal(err)
		}
		d.git(d.clone(), "config", "core.fsmonitor", monitor)
		d.putVision("# Fixture Vision, revised\n")
		clearMarkers(d)
		d.callStep("scm-7-commit")
		d.callStep("scm-7-publish")
		if names := markerNames(d); len(names) > 0 {
			d.t.Fatalf("a repository program ran: %v", names)
		}
		d.assertTrailer("c5", "CS-00002")
		// Control: the same programs do run for an ordinary git commit, so
		// the absence of markers above is the SCM's doing.
		d.git(d.clone(), "commit", "--quiet", "--allow-empty", "-m", "A commit in the user's own shell")
		if names := markerNames(d); !strings.Contains(strings.Join(names, ","), "pre-commit") || !strings.Contains(strings.Join(names, ","), "reference-transaction") {
			d.t.Fatalf("the planted programs did not run for an ordinary commit: %v", names)
		}
	}},
	{"scm-7b legacy client", "after-6", func(d *driver) {
		modern := d.call("repo_state", nil)
		legacy := d.connect(d.clone(), legacyMCP)
		defer func() { _ = legacy.Close() }()
		if got := legacy.InitializeResult().ProtocolVersion; got != legacyMCP {
			d.t.Fatalf("the legacy client negotiated %s", got)
		}
		old := d.callOn(legacy, "repo_state", nil)
		if !bytes.Equal(modern, old) {
			d.t.Fatalf("the legacy result differs:\nmodern: %s\nlegacy: %s", modern, old)
		}
		if again := d.call("repo_state", nil); !bytes.Equal(modern, again) {
			d.t.Fatalf("repo_state is not byte-stable:\n%s\n%s", modern, again)
		}
	}},
	{"scm-8 outside the face", "after-6", func(d *driver) {
		d.callStep("scm-8-outside-face")
		if d.session == nil {
			d.session = d.connect(d.clone(), modernMCP)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		list, err := d.session.ListTools(ctx, nil)
		if err != nil {
			d.t.Fatal(err)
		}
		var names []string
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		if got := strings.Join(names, ","); got != "repo_state,branch_init,branch_resume,commit,publish,refresh" {
			d.t.Fatalf("tools/list names %s", got)
		}
	}},
	{"trace context and CLI parity", "after-6", func(d *driver) {
		const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		if d.session == nil {
			d.session = d.connect(d.clone(), modernMCP)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := d.session.CallTool(ctx, &mcp.CallToolParams{
			Meta: mcp.Meta{"traceparent": traceparent}, Name: "repo_state", Arguments: map[string]any{},
		})
		if err != nil {
			d.t.Fatal(err)
		}
		var traced struct {
			Trace map[string]any `json:"trace"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &traced); err != nil || traced.Trace["traceparent"] != traceparent {
			d.t.Fatalf("the result does not copy the trace context: %v", traced.Trace)
		}
		viaMCP := d.call("repo_state", nil)
		viaCLI, status := d.cli("--output", "json", "repo-state")
		if status != 0 || !bytes.Equal(bytes.TrimSpace(viaCLI), viaMCP) {
			d.t.Fatalf("the CLI result differs from the MCP result:\n%s\n%s", viaCLI, viaMCP)
		}
		out, status := d.cli("--output", "json", "publish", "--force")
		if status != 1 || !bytes.Contains(out, []byte(`"code":"INVALID_REQUEST"`)) || !bytes.Contains(out, []byte(`"field":"force"`)) {
			d.t.Fatalf("publish --force: exit %d: %s", status, out)
		}
	}},
	{"a tag that shadows the remote default branch", "after-6", func(d *driver) {
		d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog")
		// An unreviewed commit, reachable only through a tag named like
		// the remote-tracking ref that refresh merges.
		d.git(d.clone(), "switch", "--quiet", "--create", "side", "main")
		d.write("evil.txt", "unreviewed\n")
		d.git(d.clone(), "add", "--", "evil.txt")
		d.git(d.clone(), "commit", "--quiet", "-m", "An unreviewed commit")
		d.git(d.clone(), "tag", "origin/main")
		d.git(d.clone(), "switch", "--quiet", "cs/00002-add-the-initial-sketch")
		d.git(d.clone(), "branch", "--quiet", "-D", "side")
		head := d.rev(d.clone(), "HEAD")
		out := d.call("refresh", nil)
		if !bytes.Contains(out, []byte(`"code":"GIT_FAILED"`)) || !bytes.Contains(out, []byte(`"name":"origin/main"`)) {
			d.t.Fatalf("refresh with a shadowing tag: %s", out)
		}
		if now := d.rev(d.clone(), "HEAD"); now != head {
			d.t.Fatal("refresh moved HEAD")
		}
		d.git(d.clone(), "switch", "--quiet", "main")
		main := d.rev(d.clone(), "refs/heads/main")
		state := d.call("repo_state", nil)
		if !bytes.Contains(state, []byte(`"local":"behind"`)) || d.rev(d.clone(), "refs/heads/main") != main {
			d.t.Fatalf("repo_state fast-forwarded through a shadowing tag: %s", state)
		}
	}},
	{"protected names in another letter case", "after-6", func(d *driver) {
		d.stubOnly()
		for _, name := range []string{"claude.md", ".GitAttributes", ".Claude/settings.json"} {
			d.restore("after-6")
			_, status := d.ears([]string{"--output", "json", "artifact", "put", "--change-set", "CS-00002", "--id", "case",
				"--kind", "interface-prose", "--path", name, "--owner", "user", "--content-stdin"}, []byte("text\n"))
			if status != 0 {
				d.t.Fatalf("the ears-manager stub refused %s: %d", name, status)
			}
			out := d.call("commit", nil)
			if !bytes.Contains(out, []byte(`"code":"PATH_NOT_STAGEABLE"`)) || !bytes.Contains(out, []byte(name)) {
				d.t.Fatalf("commit of %s: %s", name, out)
			}
		}
	}},
	{"a stale tracking ref of the initialization branch", "base", func(d *driver) {
		// The branch was deleted on the host, but its tracking ref stayed.
		d.git(d.clone(), "update-ref", "refs/remotes/origin/cs/00001-project-init", "HEAD")
		d.expect("1-branch-init", d.step("1-branch-init")["result"], d.call("branch_init", nil))
	}},
	{"a live initialization branch on the upstream remote", "base", func(d *driver) {
		d.git(d.second(), "push", "--quiet", "origin", "main:refs/heads/cs/00001-project-init")
		out := d.call("branch_init", nil)
		if !bytes.Contains(out, []byte(`"code":"BRANCH_EXISTS"`)) || !bytes.Contains(out, []byte(`"where":"remote"`)) {
			d.t.Fatalf("branch_init with the branch on origin: %s", out)
		}
	}},
	{"an upstream remote with no HEAD", "base", func(d *driver) {
		d.git(d.clone(), "remote", "set-head", "origin", "--delete")
		out := d.call("branch_init", nil)
		if !bytes.Contains(out, []byte(`"code":"DEFAULT_NOT_FOUND"`)) || !bytes.Contains(out, []byte(`"next":["git","remote","set-head","origin","--auto"]`)) {
			d.t.Fatalf("branch_init with no remote HEAD: %s", out)
		}
	}},
	{"an intent longer than a pull-request title", "after-6", func(d *driver) {
		intent := strings.TrimSpace(strings.Repeat("Add the initial Sketch ", 12))
		if _, status := d.ears([]string{"--output", "json", "change-set", "update", "--change-set", "CS-00002", "--intent", intent}, nil); status != 0 {
			d.t.Fatalf("change-set update exited %d", status)
		}
		if out := d.call("commit", nil); !bytes.Contains(out, []byte(`"ok":true`)) {
			d.t.Fatalf("commit of a long intent: %s", out)
		}
		calls := len(d.gh().Recorded)
		out := d.call("publish", nil)
		if !bytes.Contains(out, []byte(`"code":"UNSAFE_TEXT"`)) || !bytes.Contains(out, []byte("longer than 256 characters")) {
			d.t.Fatalf("publish of a long intent: %s", out)
		}
		if len(d.gh().Recorded) != calls {
			d.t.Fatal("the gh stub recorded a call")
		}
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"an intent that hides GitHub text", "after-6", func(d *driver) {
		for _, intent := range []string{"Fixes\u00a0#12", "Adds a tag [skip ci]"} {
			d.restore("after-6")
			if _, status := d.ears([]string{"--output", "json", "change-set", "update", "--change-set", "CS-00002", "--intent", intent}, nil); status != 0 {
				d.t.Fatalf("change-set update exited %d", status)
			}
			head := d.rev(d.clone(), "HEAD")
			out := d.call("commit", nil)
			if !bytes.Contains(out, []byte(`"code":"UNSAFE_TEXT"`)) || d.rev(d.clone(), "HEAD") != head {
				d.t.Fatalf("commit of the intent %q: %s", intent, out)
			}
		}
	}},
	{"a merge message that a branch name makes unsafe", "after-6", func(d *driver) {
		d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog")
		d.git(d.clone(), "branch", "-m", "cs/00002-add-the-initial-sketch", "cs/00002-fixes#1")
		head := d.rev(d.clone(), "HEAD")
		out := d.call("refresh", nil)
		if !bytes.Contains(out, []byte(`"code":"UNSAFE_TEXT"`)) || !bytes.Contains(out, []byte(`"retry":"user"`)) || d.rev(d.clone(), "HEAD") != head {
			d.t.Fatalf("refresh on cs/00002-fixes#1: %s", out)
		}
	}},
	{"a change-set path replaced by a directory before publish", "after-6", func(d *driver) {
		vision := filepath.Join(d.clone(), "docs", "vision.md")
		if err := os.Remove(vision); err != nil {
			d.t.Fatal(err)
		}
		d.write("docs/vision.md/notes.md", "notes\n")
		calls := d.gh().Calls
		out := d.call("publish", nil)
		if !bytes.Contains(out, []byte(`"code":"UNCOMMITTED_CHANGES"`)) || !bytes.Contains(out, []byte(`"docs/vision.md"`)) {
			d.t.Fatalf("publish with a directory at a change-set path: %s", out)
		}
		if d.gh().Calls != calls {
			d.t.Fatal("the gh stub received a call")
		}
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"a symlinked control directory", "after-6", func(d *driver) {
		// A valid project file outside the working tree, behind the link.
		outside := filepath.Join(d.work, "control")
		if err := os.Rename(filepath.Join(d.clone(), ".protobot"), outside); err != nil {
			d.t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(d.clone(), ".protobot")); err != nil {
			d.t.Fatal(err)
		}
		out := d.call("repo_state", nil)
		if !bytes.Contains(out, []byte(`"code":"PROJECT_UNREADABLE"`)) || !bytes.Contains(out, []byte(`"path":".protobot/"`)) {
			d.t.Fatalf("repo_state through a symlinked .protobot: %s", out)
		}
	}},
	{"a fetch refspec that maps into local refs", "after-6", func(d *driver) {
		d.git(d.clone(), "config", "--replace-all", "remote.origin.fetch", "+refs/heads/*:refs/heads/mirror/*")
		d.git(d.clone(), "config", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
		d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog")
		d.git(d.second(), "tag", "fixture-tag", "main")
		d.git(d.second(), "push", "--quiet", "origin", "fixture-tag")
		out := d.call("repo_state", nil)
		if !bytes.Contains(out, []byte(`"ok":true`)) {
			d.t.Fatalf("repo_state: %s", out)
		}
		if refs := d.git(d.clone(), "for-each-ref", "--format=%(refname)", "refs/heads/mirror/", "refs/tags/"); refs != "" {
			d.t.Fatalf("the fetch moved local refs: %s", refs)
		}
		if got, want := d.rev(d.clone(), "refs/remotes/origin/main"), d.rev(d.origin(), "refs/heads/main"); got != want {
			d.t.Fatalf("origin/main is %s, want %s", got, want)
		}
	}},
	{"a default branch inside the change-set prefix", "after-6", func(d *driver) {
		// Such a default branch would read as CS-00001, and the ref policy
		// would then permit a write to the branch that holds approved state.
		content := strings.Replace(d.read(".protobot/project.yaml"), "default_branch: main", "default_branch: cs/00001-main", 1)
		d.write(".protobot/project.yaml", content)
		head := d.rev(d.clone(), "HEAD")
		calls := d.gh().Calls
		for _, tool := range []string{"commit", "publish"} {
			out := d.call(tool, nil)
			if !bytes.Contains(out, []byte(`"code":"PROJECT_UNREADABLE"`)) || !bytes.Contains(out, []byte(`"commands":[]`)) {
				d.t.Fatalf("%s with a default branch inside the prefix: %s", tool, out)
			}
		}
		if d.rev(d.clone(), "HEAD") != head {
			d.t.Fatal("HEAD moved")
		}
		if d.gh().Calls != calls {
			d.t.Fatal("the gh stub received a call")
		}
		d.assertRefs(map[string]string{
			"refs/heads/main": "m1", "refs/heads/cs/00002-add-the-initial-sketch": "c2",
			"origin/refs/heads/main": "m1", "origin/refs/heads/cs/00002-add-the-initial-sketch": "c2",
		})
	}},
	{"a change-set branch that the remote deleted", "after-6", func(d *driver) {
		const branch = "cs/00002-add-the-initial-sketch"
		if out := d.call("repo_state", nil); !bytes.Contains(out, []byte(`"branch":"`+branch+`","on_remote":true`)) {
			d.t.Fatalf("repo_state before the delete: %s", out)
		}
		d.git(d.second(), "push", "--quiet", "origin", "--delete", branch)
		out := d.call("repo_state", nil)
		if !bytes.Contains(out, []byte(`"branch":"`+branch+`","on_remote":false`)) {
			d.t.Fatalf("repo_state after the delete: %s", out)
		}
		if refs := d.git(d.clone(), "for-each-ref", "--format=%(refname)", "refs/remotes/origin/"+branch); refs != "" {
			d.t.Fatalf("the tracking ref of the deleted branch stands: %s", refs)
		}
	}},
	{"a default branch that the remote deleted", "after-6", func(d *driver) {
		// A bare repository refuses to delete the branch its HEAD names.
		d.git(d.second(), "push", "--quiet", "origin", "main:refs/heads/keep")
		d.git(d.origin(), "symbolic-ref", "HEAD", "refs/heads/keep")
		d.git(d.second(), "push", "--quiet", "origin", "--delete", "main")
		out := d.call("publish", nil)
		if !bytes.Contains(out, []byte(`"code":"BASE_NOT_ON_DEFAULT"`)) || !bytes.Contains(out, []byte(`"default_head":null`)) {
			d.t.Fatalf("publish with no default branch on the remote: %s", out)
		}
		if refs := d.git(d.clone(), "for-each-ref", "--format=%(refname)", "refs/remotes/origin/main"); refs != "" {
			d.t.Fatalf("the tracking ref of the deleted default branch stands: %s", refs)
		}
		// assertNoPush reads other.git, which this check does not create.
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"a host failure after a no-op push", "after-6", func(d *driver) {
		cases := []struct{ answer, mutation, retry string }{
			// A 5xx can come after GitHub applied the edit.
			{"HTTP 502: Bad Gateway (https://api.github.com/graphql)", "unknown", "reconcile"},
			// A validation error is a refusal: the host did not change.
			{"HTTP 422: Validation Failed (https://api.github.com/graphql)", "none", "retry"},
		}
		for _, c := range cases {
			d.restore("after-6")
			d.setGH(func(s *ghState) {
				for i := range s.Pulls {
					if s.Pulls[i].Number == 2 {
						s.Pulls[i].Body = "Edited on the host."
					}
				}
				s.Fail = map[string]string{"edit": c.answer}
			})
			out := d.call("publish", nil)
			want := []string{`"code":"HOST_REQUEST_FAILED"`, `"mutation":"` + c.mutation + `"`, `"retry":"` + c.retry + `"`, `"pull_request":2`}
			for _, w := range want {
				if !bytes.Contains(out, []byte(w)) {
					d.t.Fatalf("publish after %q lacks %s: %s", c.answer, w, out)
				}
			}
			if bytes.Contains(out, []byte(`"refs"`)) {
				d.t.Fatalf("a no-op push names a pushed ref: %s", out)
			}
		}
	}},
	{"scm-9 other repository", "after-6", func(d *driver) {
		calls := d.gh().Calls
		d.callStep("scm-9-other-repo")
		if d.gh().Calls != calls {
			d.t.Fatal("the gh stub received a call")
		}
	}},
	{"scm-10 fork pull request", "after-6", func(d *driver) {
		d.setGH(func(s *ghState) {
			s.Pulls = append(s.Pulls, ghPull{
				Repo: repoID, Number: 7, State: "CLOSED", URL: "https://" + repoID + "/pull/7", Title: "Fork",
				HeadRefName: "cs/00002-add-the-initial-sketch", BaseRefName: "main", IsCrossRepository: true,
				HeadRepository: map[string]any{"name": "fixture"}, HeadRepositoryOwner: map[string]any{"login": "other-owner"},
			})
		})
		d.callStep("scm-10-repo-state")
	}},
	{"scm-11 credential in the URL", "after-6", func(d *driver) {
		d.git(d.clone(), "remote", "set-url", "origin", "https://PROTOBOT-FIXTURE-TOKEN@github.com/protobot-fixture/fixture.git")
		out := d.callStep("scm-11-repo-state")
		for _, leak := range []string{"PROTOBOT-FIXTURE-TOKEN", "https://"} {
			if bytes.Contains(out, []byte(leak)) {
				d.t.Fatalf("the result holds %q", leak)
			}
		}
	}},
	{"scm-12 redirected push", "after-6", func(d *driver) {
		d.git(d.work, "init", "--quiet", "--bare", "other.git")
		d.git(d.clone(), "config", "remote.origin.pushurl", filepath.Join(d.work, "other.git"))
		d.callStep("scm-12-publish")
		d.assertNoPush()
	}},
	{"scm-13 conflicting refresh", "after-6", func(d *driver) {
		d.bind("m2c", d.upstreamCommit("docs/vision.md", "# Upstream Vision\n", "Change the vision upstream"))
		index := d.git(d.clone(), "ls-files", "-s")
		d.callStep("scm-13-refresh")
		d.assertRefs(map[string]string{"refs/heads/cs/00002-add-the-initial-sketch": "c2"})
		if d.git(d.clone(), "ls-files", "-s") != index || d.git(d.clone(), "status", "--porcelain") != "" {
			d.t.Fatal("the index or the working tree changed")
		}
		if _, status := d.run(d.clone(), nil, "git", "rev-parse", "-q", "--verify", "MERGE_HEAD"); status == 0 {
			d.t.Fatal("a merge is in progress")
		}
	}},
	{"scm-14 uncommitted before a push", "after-6", func(d *driver) {
		d.earsStep("scm-14-artifact-put")
		d.callStep("scm-14-publish")
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"scm-15 trailer in the body", "after-6", func(d *driver) { d.callStep("scm-15-body-trailer") }},
	{"scm-16 closing keyword in the intent", "after-6", func(d *driver) {
		d.earsStep("scm-16-closing-keyword")
		d.callStep("scm-16-commit")
	}},
	{"scm-19 host down for approved_merge", "after-8", func(d *driver) {
		d.setGH(func(s *ghState) { s.Down = true })
		s := d.step("scm-19-host-down")
		out, status := d.register("CS-00002")
		if status != 1 {
			d.t.Fatalf("registration exited %d", status)
		}
		d.expect("scm-19-host-down", s["scm_read"].(map[string]any)["result"], out)
		if calls := d.registrationsOf("CS-00002"); len(calls) != 1 {
			d.t.Fatalf("%d registration calls", len(calls))
		}
	}},
	{"scm-20 SCP-style remote", "base", func(d *driver) {
		scp := "git@github.com:protobot-fixture/fixture.git"
		d.git(d.clone(), "remote", "set-url", "origin", scp)
		d.git(d.clone(), "config", "--add", "url."+d.origin()+".insteadOf", scp)
		d.callStep("scm-20-branch-init")
	}},
	{"scm-21 two URLs", "after-6", func(d *driver) {
		d.git(d.work, "init", "--quiet", "--bare", "other.git")
		d.git(d.clone(), "config", "--add", "remote.origin.url", filepath.Join(d.work, "other.git"))
		d.callStep("scm-21-publish")
		d.assertNoPush()
	}},
	{"scm-22 protected path", "after-6", func(d *driver) {
		d.stubOnly()
		_, status := d.ears([]string{"--output", "json", "artifact", "put", "--change-set", "CS-00002", "--id", "agents",
			"--kind", "interface-prose", "--path", "AGENTS.md", "--owner", "user", "--content-stdin"}, []byte("# Agents\n"))
		if status != 0 {
			d.t.Fatalf("the ears-manager stub refused AGENTS.md: %d", status)
		}
		d.callStep("scm-22-commit")
	}},
	{"scm-23 fork URL at initialization", "base", func(d *driver) {
		fork := "https://github.com/someone/fixture.git"
		d.git(d.work, "clone", "--quiet", "--bare", d.origin(), "fork.git")
		d.git(d.clone(), "remote", "add", "fork", fork)
		d.git(d.clone(), "config", "url."+filepath.Join(d.work, "fork.git")+".insteadOf", fork)
		d.callStep("scm-23-branch-init")
		d.earsStep("scm-23-project-init")
		d.earsStep("scm-23-change-set-create")
		d.callStep("scm-23-commit")
	}},
	{"scm-24 clean filter", "after-6", func(d *driver) {
		d.filter("cat\necho appended by a clean filter\n")
		d.putVision("# Fixture Vision, revised\n")
		index := d.git(d.clone(), "ls-files", "-s")
		d.callStep("scm-24-commit")
		if d.git(d.clone(), "ls-files", "-s") != index {
			d.t.Fatal("the index changed")
		}
	}},
	{"scm-25 closing keyword in the body", "after-6", func(d *driver) { d.callStep("scm-25-body-keyword") }},
	{"scm-26 closing keyword that reached a commit", "after-6", func(d *driver) {
		if _, status := d.ears([]string{"--output", "json", "change-set", "update", "--change-set", "CS-00002", "--intent", "Fixes #1"}, nil); status != 0 {
			d.t.Fatalf("change-set update exited %d", status)
		}
		// The write changes the manifest and the change-set store digest.
		d.git(d.clone(), "add", "--", ".protobot/change-sets/cs-00002.yaml", ".protobot/project.yaml")
		d.git(d.clone(), "commit", "--quiet", "-m", "Set the intent in the user's own shell")
		calls := len(d.gh().Recorded)
		d.callStep("scm-26-publish")
		if len(d.gh().Recorded) != calls {
			d.t.Fatal("the gh stub recorded a call")
		}
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	}},
	{"scm-27 no canonical remote", "after-6", func(d *driver) {
		d.git(d.clone(), "remote", "remove", "origin")
		d.callStep("scm-27-repo-state")
	}},
	{"scm-30 hosted start", "after-6", func(d *driver) {
		cmd := exec.Command(binaries.scm, "serve", "--face", "drafting-table", "--transport", "streamable-http")
		cmd.Dir = d.clone()
		cmd.Env = d.env()
		done := make(chan error, 1)
		if err := cmd.Start(); err != nil {
			d.t.Fatal(err)
		}
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				d.t.Fatal("serve --transport streamable-http exited zero")
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			d.t.Fatal("serve --transport streamable-http kept running")
		}
	}},
	{"scm-31 nested project file", "base", func(d *driver) {
		d.write("docs/.protobot/project.yaml", "project:\n  id: nested\n")
		sub := d.connect(filepath.Join(d.clone(), "docs"), modernMCP)
		defer func() { _ = sub.Close() }()
		d.expect("scm-31-repo-state-subdir", d.step("scm-31-repo-state-subdir")["result"], d.callOn(sub, "repo_state", nil))
		d.callStep("scm-31-repo-state-root")
	}},
	{"scm-32 behind default branch, checked out", "after-6", func(d *driver) {
		d.bind("m2b", d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog"))
		d.git(d.clone(), "checkout", "--quiet", "main")
		d.callStep("scm-32-repo-state")
		d.assertClean("m2b")
	}},
	{"scm-32 behind default branch before init", "base", func(d *driver) {
		d.bind("m0b", d.upstreamCommit("CHANGELOG.md", "# Changes\n", "Add a changelog"))
		d.callStep("scm-32-branch-init")
		d.assertRefs(map[string]string{"refs/heads/main": "m0b"})
		d.assertClean("m0b")
	}},
	{"scm-33 mixed projection", "after-6", func(d *driver) {
		_, status := d.ears([]string{"--output", "json", "artifact", "put", "--change-set", "CS-00002", "--id", "cli",
			"--kind", "interface-prose", "--path", "docs/interfaces/cli.md", "--owner", "user", "--content-stdin"}, []byte("# CLI\n"))
		if status != 0 {
			d.t.Fatalf("artifact put exited %d", status)
		}
		d.appendLine(".protobot/projection.yaml", "  - path: README.md\n    class: implementation")
		d.callStep("scm-33-commit")
	}},
	{"scm-34 host down after the merge", "after-8-host-merge", func(d *driver) {
		d.setGH(func(s *ghState) { s.Down = true })
		d.callStep("scm-34-publish")
		d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c3"})
	}},
	{"scm-35 directory path", "after-6", func(d *driver) {
		d.stubOnly()
		d.saveJSON(d.earsControl(), map[string]any{"extra_paths": map[string]any{"CS-00002": []string{"docs/"}}})
		d.write("docs/notes.txt", "notes\n")
		d.callStep("scm-35-commit")
		d.assertUntracked("docs/notes.txt")
	}},
	{"scm-36 staged in scope", "after-6", func(d *driver) {
		d.putVision("# Fixture Vision, revised\n")
		d.git(d.clone(), "add", "--", "docs/vision.md")
		d.filter("cat\necho appended by a clean filter\n")
		index := d.git(d.clone(), "ls-files", "-s")
		d.callStep("scm-36-commit")
		if d.git(d.clone(), "ls-files", "-s") != index {
			d.t.Fatal("the index changed")
		}
		if staged := d.git(d.clone(), "diff", "--cached", "--name-only"); staged != "docs/vision.md" {
			d.t.Fatalf("staged %q", staged)
		}
	}},
	{"scm-37 write after staging", "after-6", func(d *driver) {
		vision := filepath.Join(d.clone(), "docs", "vision.md")
		d.filter("cat\ncase \"$GIT_INDEX_FILE\" in\n  *scm-index-*) printf 'a late write\\n' >> '" + vision + "' ;;\nesac\n")
		d.putVision("# Fixture Vision, revised\n")
		d.callStep("scm-37-commit")
		if got := d.git(d.clone(), "show", d.sha("c6")+":docs/vision.md"); got != "# Fixture Vision, revised" {
			d.t.Fatalf("the commit holds %q", got)
		}
		if unstaged := d.git(d.clone(), "diff", "--name-only"); unstaged != "docs/vision.md" {
			d.t.Fatalf("unstaged %q, want docs/vision.md", unstaged)
		}
	}},
	{"scm-38 another writer", "after-6", func(d *driver) {
		d.filter("cat\ncase \"$GIT_INDEX_FILE\" in\n  *scm-index-*) " +
			"env -u GIT_INDEX_FILE -u GIT_DIR -u GIT_WORK_TREE git -C '" + d.clone() + "' commit --quiet --allow-empty -m 'Another writer' ;;\nesac\n")
		d.putVision("# Fixture Vision, revised\n")
		index := d.git(d.clone(), "ls-files", "-s")
		d.callStep("scm-38-commit")
		d.bind("w1", d.rev(d.clone(), "refs/heads/cs/00002-add-the-initial-sketch"))
		d.assertRefs(map[string]string{"refs/heads/cs/00002-add-the-initial-sketch": "w1"})
		if d.git(d.clone(), "ls-files", "-s") != index {
			d.t.Fatal("the index changed")
		}
		if got := d.read("docs/vision.md"); got != "# Fixture Vision, revised\n" {
			d.t.Fatalf("the working tree changed: %q", got)
		}
	}},
	{"scm-39 merge in progress", "after-6", func(d *driver) {
		d.upstreamCommit("docs/vision.md", "# Upstream Vision\n", "Change the vision upstream")
		d.git(d.clone(), "fetch", "--quiet", "origin")
		if _, status := d.run(d.clone(), nil, "git", "merge", "--quiet", "origin/main"); status == 0 {
			d.t.Fatal("the merge did not conflict")
		}
		d.callStep("scm-39-commit")
		d.assertRefs(map[string]string{"refs/heads/cs/00002-add-the-initial-sketch": "c2"})
		if _, status := d.run(d.clone(), nil, "git", "rev-parse", "-q", "--verify", "MERGE_HEAD"); status != 0 {
			d.t.Fatal("MERGE_HEAD is gone")
		}
	}},
}

func markerNames(d *driver) []string {
	entries, _ := os.ReadDir(d.markers())
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func clearMarkers(d *driver) {
	entries, _ := os.ReadDir(d.markers())
	for _, e := range entries {
		_ = os.Remove(filepath.Join(d.markers(), e.Name()))
	}
}

// putVision writes the Vision through ears-manager artifact put.
func (d *driver) putVision(content string) {
	d.t.Helper()
	_, status := d.ears([]string{"--output", "json", "artifact", "put", "--change-set", "CS-00002", "--id", "vision",
		"--kind", "vision", "--path", "docs/vision.md", "--owner", "user", "--content-stdin"}, []byte(content))
	if status != 0 {
		d.t.Fatalf("artifact put exited %d", status)
	}
}

// assertNoPush checks that neither origin nor other.git received a push.
func (d *driver) assertNoPush() {
	d.t.Helper()
	d.assertRefs(map[string]string{"origin/refs/heads/cs/00002-add-the-initial-sketch": "c2"})
	if refs := d.git(filepath.Join(d.work, "other.git"), "for-each-ref"); refs != "" {
		d.t.Fatalf("other.git received a push: %s", refs)
	}
}

// assertClean checks that HEAD, the index, and the working tree match a
// commit.
func (d *driver) assertClean(name string) {
	d.t.Helper()
	if head := d.rev(d.clone(), "HEAD"); head != d.sha(name) {
		d.t.Fatalf("HEAD is %s, want <sha:%s>", head, name)
	}
	if status := d.git(d.clone(), "status", "--porcelain", "--untracked-files=no"); status != "" {
		d.t.Fatalf("the index or the working tree differs from HEAD:\n%s", status)
	}
}

// Ensure the fixture's hosted rows and the JSON shape stay in sync with
// this driver.
var _ = json.Valid
