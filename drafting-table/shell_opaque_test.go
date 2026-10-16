package guard

import "testing"

func TestGitExecutionConfigDetection(t *testing.T) {
	t.Run("keys", func(t *testing.T) {
		for _, key := range []string{
			"diff.external", "diff.converter.textconv", "diff.converter.command",
			"merge.driver.driver", "gpg.program", "gpg.ssh.program",
			"remote.origin.uploadpack", "remote.origin.receivepack",
			"uploadpack.packobjectshook", "include.path", "includeIf.gitdir:/repo.path",
			"sendemail.smtpserver", "sendemail.work.smtpserver", "submodule.example.update",
			"man.viewer", "man.probe.cmd", "man.probe.path",
			"browser.probe.cmd", "browser.probe.path", "help.browser", "web.browser", "instaweb.httpd",
			"Core.alternateRefsCommand", "TRAILER.X.CMD", "trailer.x.command", "TRAILER..CMD", "trailer..command",
			"remote.pwn.vcs", "remote..vcs",
			"protocol.allow", "protocol.ext.allow", "protocol..allow",
			"url.https://example.com/.insteadOf", "url.https://example.com/.pushInsteadOf",
			"http.extraHeader", "http.proxy", "http..extraHeader", "http..proxy",
			"http.https://example.com/.extraHeader", "http.https://example.com/.proxy",
		} {
			if !gitExecutionConfigKey(key) {
				t.Errorf("gitExecutionConfigKey(%q) = false", key)
			}
		}
		for _, key := range []string{
			"remote.origin.url", "help.autocorrect", "web.autocorrect", "instaweb.local",
			"protocol.version", "trailer.x.key", "http.sslVerify",
			"http.https://example.com/.sslVerify", "http.proxyAuthMethod",
		} {
			if gitExecutionConfigKey(key) {
				t.Errorf("gitExecutionConfigKey(%q) = true", key)
			}
		}
	})

	t.Run("sections", func(t *testing.T) {
		for _, section := range []string{
			"diff", "diff.converter", "merge.driver", "gpg.ssh",
			"remote.origin", "uploadpack", "include", "includeIf.gitdir:/repo",
			"sendemail", "sendemail.work", "submodule", "submodule.example",
			"man", "man.probe", "browser", "browser.probe", "help", "web", "web.browser", "instaweb", "instaweb.httpd",
			"core", "trailer", "trailer.x", "url", "url.https://example.com", "http", "http.https://example.com",
			"protocol", "protocol.ext",
		} {
			if !gitExecutionConfigSection(section) {
				t.Errorf("gitExecutionConfigSection(%q) = false", section)
			}
		}
	})

	t.Run("patterns", func(t *testing.T) {
		for _, pattern := range []string{
			`^diff\.external$`, `^diff\..*\.textconv$`, `^diff\..*\.command$`,
			`^merge\..*\.driver$`, `^gpg(\..*)?\.program$`,
			`^remote\..*\.(uploadpack|receivepack|vcs)$`, `^uploadpack\.packobjectshook$`,
			`^include\.path$`, `^includeIf\..*\.path$`,
			`^sendemail\.smtpserver$`, `^sendemail\..*\.smtpserver$`, `^help\..*$`, `^submodule\..*\.update$`,
			`^man\.viewer$`, `^man\..*\.(cmd|path)$`,
			`^browser\..*\.(cmd|path)$`, `^help\.browser$`, `^web\.browser$`, `^instaweb\.httpd$`,
			`^core\.alternateRefsCommand$`, `^trailer\..*\.(cmd|command)$`,
			`^protocol\.allow$`, `^protocol\..*\.allow$`, `^protocol\.version$`,
			`^url\..*\.(insteadOf|pushInsteadOf)$`, `^http\.extraHeader$`, `^http\.proxy$`,
			`^http\..*\.extraHeader$`, `^http\..*\.proxy$`, `^http\..*$`,
		} {
			if !gitExecutionConfigPattern(pattern) {
				t.Errorf("gitExecutionConfigPattern(%q) = false", pattern)
			}
		}
	})
	t.Run("http patterns stay focused on transport and credential keys", func(t *testing.T) {
		for _, pattern := range []string{`^http\.sslVerify$`, `^http\..*\.sslVerify$`, `^curl\..*$`} {
			if gitExecutionConfigPattern(pattern) {
				t.Errorf("gitExecutionConfigPattern(%q) = true", pattern)
			}
		}
		if !gitExecutionConfigPattern(`^http\.https://service\.invalid/\.p.*$`) {
			t.Fatal("an unmodeled HTTP regexp that may target proxy was accepted")
		}
	})
}

func TestGitInlineExecutionConfigForms(t *testing.T) {
	for _, args := range [][]string{
		{"git", "-c", "core.alternateRefsCommand=command", "upload-pack", "."},
		{"git", "-ccore.alternateRefsCommand=command", "upload-pack", "."},
		{"git", "--config-env", "trailer.x.cmd=TRAILER_CMD", "interpret-trailers", "--trailer", "x=y"},
		{"git", "--config-env=trailer.x.command=TRAILER_COMMAND", "interpret-trailers", "--trailer", "x=y"},
		{"git", "-c", "trailer..cmd=true", "interpret-trailers", "--trailer", "x=y"},
		{"git", "-c", "trailer..command=true", "interpret-trailers", "--trailer", "x=y"},
		{"git", "-c", "url.https://mirror.invalid/.insteadOf=https://origin.invalid/", "status"},
		{"git", "-c", "url.https://mirror.invalid/.pushInsteadOf=https://origin.invalid/", "status"},
		{"git", "--config-env", "url.https://mirror.invalid/.insteadOf=URL_REWRITE", "status"},
		{"git", "-c", "http.extraHeader=Authorization: token", "status"},
		{"git", "-c", "http.proxy=http://proxy.invalid", "status"},
		{"git", "-c", "http..extraHeader=safe", "status"},
		{"git", "-c", "http..proxy=http://proxy.invalid", "status"},
		{"git", "-c", "http.https://example.invalid/.extraHeader=Authorization: token", "status"},
		{"git", "--config-env=http.https://example.invalid/.proxy=GIT_HTTP_PROXY", "status"},
		{"git", "-c", "remote.pwn.vcs=ext", "status"},
		{"git", "--config-env", "remote.pwn.vcs=REMOTE_VCS", "status"},
		{"git", "-c", "remote.pwn.url=ext::helper", "status"},
		{"git", "-c", "remote.pwn.url=hg::helper", "status"},
		{"git", "--config-env", "remote.pwn.url=REMOTE_URL", "status"},
		{"git", "--config-env=remote.pwn.url=REMOTE_URL", "status"},
		{"git", "-c", "protocol.ext.allow=always", "ls-remote", "ext::helper"},
		{"git", "--config-env", "protocol.ext.allow=GIT_PROTOCOL_ALLOW", "ls-remote", "ext::helper"},
		{"git", "--config-env=protocol.allow=GIT_PROTOCOL_ALLOW", "ls-remote", "origin"},
	} {
		if !gitInvocationOpaque(args) {
			t.Errorf("gitInvocationOpaque(%q) = false", args)
		}
	}
	for _, args := range [][]string{
		{"git", "upload-pack", "."},
		{"git", "interpret-trailers", "--trailer", "x=y"},
		{"git", "-c", "color.ui=always", "status"},
		{"git", "--config-env=color.ui=COLOR", "status"},
		{"git", "-c", "protocol.version=2", "status"},
		{"git", "-c", "remote.origin.url=https://example.invalid/repository.git", "status"},
		{"git", "-c", "remote.origin.url=ssh://[::1]/repository.git", "status"},
	} {
		if gitInvocationOpaque(args) {
			t.Errorf("gitInvocationOpaque(%q) = true", args)
		}
	}
}

func TestGitLsRemoteHelperTransportDetection(t *testing.T) {
	for _, args := range [][]string{
		{"git", "ls-remote", "ext::helper", "refs/heads/main"},
		{"git", "ls-remote", "--", "ext::helper"},
		{"git", "ls-remote", "hg::helper"},
		{"git", "ls-remote", "--", "hg::helper"},
		{"git", "ls-remote", "--heads", "--upload-pack", "git-upload-pack", "ext::helper"},
		{"git", "ls-remote", "--upload-pack=git-upload-pack", "ext::helper"},
		{"git", "ls-remote", "--upload-pack", "git-upload-pack", "origin"},
		{"git", "ls-remote", "--upload-pack=git-upload-pack", "origin"},
		{"git", "ls-remote", "--exec", "git-upload-pack", "origin"},
		{"git", "ls-remote", "--exec=git-upload-pack", "origin"},
		{"git", "ls-remote", "--server-option", "agent=one", "ext::helper"},
		{"git", "ls-remote", "--server-option=agent=one", "ext::helper"},
		{"git", "ls-remote", "-o", "agent=one", "ext::helper"},
		{"git", "ls-remote", "--sort", "version:refname", "ext::helper"},
		{"git", "ls-remote", "--unknown-option", "ext::helper"},
	} {
		if !gitInvocationOpaque(args) {
			t.Errorf("gitInvocationOpaque(%q) = false", args)
		}
	}

	for _, args := range [][]string{
		{"git", "ls-remote", "origin"},
		{"git", "ls-remote", "https://example.invalid/repository.git"},
		{"git", "ls-remote", "ssh://[::1]/repository.git"},
		{"git", "ls-remote", "--branches", "--sort=version:refname", "origin"},
		{"git", "ls-remote", "-t", "origin"},
		{"git", "ls-remote", "-qt", "origin"},
		{"git", "ls-remote", "-b", "https://example.invalid/repository.git"},
		{"git", "ls-remote", "--", "https://example.invalid/repository.git"},
	} {
		if gitInvocationOpaque(args) {
			t.Errorf("gitInvocationOpaque(%q) = true", args)
		}
	}
}

func TestGitFetchPackAndSendPackAreOpaque(t *testing.T) {
	for _, args := range [][]string{
		{"git", "fetch-pack", "origin"},
		{"git", "send-pack", "origin"},
		{"git", "fetch-pack", "--exec", "git-upload-pack", "origin"},
		{"git", "send-pack", "--receive-pack", "git-receive-pack", "origin"},
		{"git", "fetch-pack", "--exec=/tmp/guard-upload-pack", "origin"},
		{"git", "fetch-pack", "--upload-pack", "/tmp/guard-upload-pack", "origin"},
		{"git", "send-pack", "--receive-pack=/tmp/guard-receive-pack", "origin"},
		{"git", "send-pack", "--exec=/tmp/guard-receive-pack", "origin"},
	} {
		if !gitInvocationOpaque(args) {
			t.Errorf("gitInvocationOpaque(%q) = false", args)
		}
	}
}

func TestGitPersistentExecutionConfigMutations(t *testing.T) {
	for _, configArgs := range [][]string{
		{"core.alternateRefsCommand", "command"},
		{"--global", "--add", "core.alternateRefsCommand", "command"},
		{"--add", "trailer.x.cmd", "command"},
		{"--global", "trailer.x.command", "command"},
		{"--system", "--add", "TRAILER.X.CMD", "command"},
		{"--global", "trailer..cmd", "true"},
		{"--system", "trailer..command", "true"},
		{"--global", "url.https://mirror.invalid/.insteadOf", "https://origin.invalid/"},
		{"--system", "--add", "url.https://mirror.invalid/.pushInsteadOf", "https://origin.invalid/"},
		{"--global", "http.extraHeader", "Authorization: token"},
		{"--system", "http.proxy", "http://proxy.invalid"},
		{"--global", "http..extraHeader", "safe"},
		{"--system", "http..proxy", "http://proxy.invalid"},
		{"--global", "http.https://example.invalid/.extraHeader", "Authorization: token"},
		{"--system", "http.https://example.invalid/.proxy", "http://proxy.invalid"},
		{"--global", "protocol.ext.allow", "always"},
		{"--global", "protocol.allow", "always"},
		{"--system", "protocol.ext.allow", "always"},
		{"--local", "protocol.ext.allow", "always"},
		{"--worktree", "protocol.ext.allow", "always"},
		{"--file", "/tmp/guard.gitconfig", "protocol.ext.allow", "always"},
		{"remote.pwn.vcs", "ext"},
		{"--global", "remote.pwn.vcs", "ext"},
		{"--system", "remote.pwn.vcs", "ext"},
		{"--local", "remote.pwn.vcs", "ext"},
		{"--worktree", "remote.pwn.vcs", "ext"},
		{"--file", "/tmp/guard.gitconfig", "remote.pwn.vcs", "ext"},
		{"--global", "remote.pwn.url", "ext::helper"},
		{"--local", "remote.pwn.url", "hg::helper"},
		{"--system", "remote.pwn.url", "hg::helper"},
		{"--global", "--add", "remote.pwn.url", "ext::helper"},
		{"--global", "--replace-all", "remote.pwn.url", "hg::helper"},
		{"set", "--global", "remote.pwn.url", "ext::helper"},
		{"--unset", "core.alternateRefsCommand"},
		{"--unset-all", "trailer.x.cmd"},
		{"--global", "--unset", "url.https://mirror.invalid/.insteadOf"},
		{"--global", "--unset", "http.extraHeader"},
		{"--global", "--unset-regexp", `^trailer\..*\.(cmd|command)$`},
		{"--system", "--unset-regexp", `^url\..*\.(insteadOf|pushInsteadOf)$`},
		{"--global", "--unset-regexp", `^protocol\..*\.allow$`},
		{"--global", "--unset-regexp", `^http\..*\.extraHeader$`},
		{"--system", "--unset-regexp", `^http\..*\.proxy$`},
		{"--rename-section", "trailer.x", "trailer.y"},
		{"--rename-section", "url.https://origin.invalid", "url.https://mirror.invalid"},
		{"--rename-section", "http.https://service.invalid/", "http.https://mirror.invalid/"},
		{"--rename-section", "protocol.ext", "protocol.file"},
		{"--remove-section", "core"},
		{"--global", "--remove-section", "http.https://example.invalid"},
		{"--remove-section", "protocol"},
		{"rename-section", "--global", "trailer.x", "trailer.y"},
		{"remove-section", "--global", "http.https://example.invalid/"},
		{"remove-section", "--global", "url"},
		{"remove-section", "--global", "trailer"},
		{"edit", "--global"},
		{"set", "--global", "trailer.x.command", "command"},
		{"unset", "--global", "trailer.x.command"},
	} {
		if !gitConfigCommandWritesExecutableConfig(configArgs) {
			t.Errorf("gitConfigCommandWritesExecutableConfig(%q) = false", configArgs)
		}
		if !gitInvocationOpaque(append([]string{"git", "config"}, configArgs...)) {
			t.Errorf("git config command %q was not opaque", configArgs)
		}
	}
	for _, configArgs := range [][]string{
		{"--global", "http.sslVerify", "true"},
		{"--global", "color.ui", "always"},
		{"--global", "protocol.version", "2"},
		{"--global", "remote.origin.url", "https://example.invalid/repository.git"},
		{"set", "--global", "color.ui", "always"},
		{"unset", "--global", "color.ui"},
	} {
		if gitConfigCommandWritesExecutableConfig(configArgs) {
			t.Errorf("gitConfigCommandWritesExecutableConfig(%q) = true", configArgs)
		}
		if gitInvocationOpaque(append([]string{"git", "config"}, configArgs...)) {
			t.Errorf("benign git config command %q was opaque", configArgs)
		}
	}
	for _, configArgs := range [][]string{
		{"get", "--global", "color.ui"},
		{"list", "--global"},
		{"--get-regexp", `^trailer\.`},
	} {
		if gitConfigCommandWritesExecutableConfig(configArgs) {
			t.Errorf("read-only git config command %q was considered executable", configArgs)
		}
		if gitInvocationOpaque(append([]string{"git", "config"}, configArgs...)) {
			t.Errorf("read-only git config command %q was opaque", configArgs)
		}
	}
}

func TestGitRemoteHelperURLSetAndUnsetActions(t *testing.T) {
	tests := []struct {
		name       string
		configArgs []string
		opaque     bool
	}{
		{
			name:       "legacy unset-all value pattern",
			configArgs: []string{"--global", "--unset-all", "remote.pwn.url", `^ext::`},
		},
		{
			name:       "modern unset value pattern",
			configArgs: []string{"unset", "--global", "remote.pwn.url", `^ext::`},
		},
		{
			name:       "legacy helper URL assignment",
			configArgs: []string{"--global", "remote.pwn.url", "ext::helper"},
			opaque:     true,
		},
		{
			name:       "add helper URL",
			configArgs: []string{"--global", "--add", "remote.pwn.url", "ext::helper"},
			opaque:     true,
		},
		{
			name:       "replace-all helper URL",
			configArgs: []string{"--global", "--replace-all", "remote.pwn.url", "ext::helper"},
			opaque:     true,
		},
		{
			name:       "modern set helper URL",
			configArgs: []string{"set", "--global", "remote.pwn.url", "ext::helper"},
			opaque:     true,
		},
		{
			name:       "remote VCS deletion remains protected",
			configArgs: []string{"--global", "--unset-all", "remote.pwn.vcs", `^ext::`},
			opaque:     true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := append([]string{"git", "config"}, test.configArgs...)
			if got := gitConfigCommandWritesExecutableConfig(test.configArgs); got != test.opaque {
				t.Errorf("gitConfigCommandWritesExecutableConfig(%q) = %t, want %t", test.configArgs, got, test.opaque)
			}
			if got := gitInvocationOpaque(command); got != test.opaque {
				t.Errorf("gitInvocationOpaque(%q) = %t, want %t", command, got, test.opaque)
			}
		})
	}
}

func TestGitConfigUnsetRegexpAlwaysFailsClosed(t *testing.T) {
	patterns := []string{
		`^trail[e]r\..*\.cmd$`,
		`^u.l\..*\.insteadOf$`,
		`^http\.https://service\.invalid/\.p.*$`,
		`^protocol\..*\.allow$`,
		`^color\..*$`,
		`^http\.sslVerify$`,
	}
	for _, pattern := range patterns {
		configArgs := []string{"--global", "--unset-regexp", pattern}
		command := append([]string{"git", "config"}, configArgs...)
		t.Run(pattern, func(t *testing.T) {
			if !gitConfigCommandWritesExecutableConfig(configArgs) {
				t.Error("unset-regexp was not classified as executable config mutation")
			}
			if !gitInvocationOpaque(command) {
				t.Error("unset-regexp was not opaque")
			}
			if _, safe := gitConfigWriteTargets(command, projectState{}, ""); safe {
				t.Error("unset-regexp was accepted by target-write parsing")
			}
		})
	}
	if gitConfigCommandWritesExecutableConfig([]string{"--get-regexp", `^trailer\.`}) {
		t.Fatal("read-only --get-regexp was classified as a write")
	}
}

func TestGitConfigReadActionsAndWriteTargets(t *testing.T) {
	for _, args := range [][]string{
		{"git", "config", "get", "--global", "color.ui"},
		{"git", "config", "list", "--global"},
		{"git", "config", "set", "--global", "color.ui", "always"},
		{"git", "config", "unset", "--global", "color.ui"},
	} {
		if gitInvocationOpaque(args) {
			t.Errorf("benign or read-only git config action %q was opaque", args)
		}
		targets, safe := gitConfigWriteTargets(args, projectState{}, "")
		if !safe || len(targets) != 0 {
			t.Errorf("benign or read-only git config action %q targets = %v, safe=%v", args, targets, safe)
		}
	}

	for _, args := range [][]string{
		{"git", "config", "rename-section", "--global", "trailer.x", "trailer.y"},
		{"git", "config", "remove-section", "--global", "http.https://service.invalid/"},
		{"git", "config", "edit", "--global"},
		{"git", "config", "set", "--global", "trailer.x.command", "command"},
		{"git", "config", "unset", "--global", "trailer.x.command"},
	} {
		if !gitInvocationOpaque(args) {
			t.Errorf("unsafe git config action %q was not opaque", args)
		}
		if _, safe := gitConfigWriteTargets(args, projectState{}, ""); safe {
			t.Errorf("unsafe git config action %q was accepted by target-write parsing", args)
		}
	}
}

func TestGitEmptyExecutionConfigKeysAreRejectedBeforeBuiltinClassification(t *testing.T) {
	tests := []struct {
		args    []string
		builtin string
	}{
		{[]string{"git", "-c", "trailer..cmd=true", "interpret-trailers", "--trailer", "x=y"}, "interpret-trailers"},
		{[]string{"git", "-c", "trailer..command=true", "interpret-trailers", "--trailer", "x=y"}, "interpret-trailers"},
		{[]string{"git", "config", "--global", "trailer..cmd", "true"}, "config"},
		{[]string{"git", "config", "--global", "trailer..command", "true"}, "config"},
		{[]string{"git", "-c", "http..extraHeader=safe", "status"}, "status"},
		{[]string{"git", "-c", "http..proxy=safe", "status"}, "status"},
		{[]string{"git", "-c", "protocol..allow=always", "ls-remote", "origin"}, "ls-remote"},
		{[]string{"git", "config", "--global", "http..extraHeader", "safe"}, "config"},
		{[]string{"git", "config", "--global", "http..proxy", "safe"}, "config"},
		{[]string{"git", "config", "--global", "protocol..allow", "always"}, "config"},
	}
	for _, test := range tests {
		if !gitBuiltins[test.builtin] {
			t.Fatalf("test requires git builtin %q", test.builtin)
		}
		if !gitInvocationOpaque(test.args) {
			t.Errorf("execution-sensitive config on builtin %q was not rejected: %q", test.builtin, test.args)
		}
	}
}

func TestRipgrepConfigEnvironmentMakesInvocationOpaque(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "/tmp/rg-config")
	if !opaqueGovernedExecution("rg", []string{"rg", "."}) {
		t.Fatal("ripgrep with an environment config path was considered inspectable")
	}
}

func TestCommandExecutionEnvironmentOverrides(t *testing.T) {
	for _, key := range []string{
		"GIT_SSH_COMMAND", "GIT_SSH", "GIT_ASKPASS", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE",
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR", "GIT_CEILING_DIRECTORIES",
		"GIT_ALLOW_PROTOCOL", "GIT_PROXY_COMMAND",
	} {
		if !commandExecutionEnvironmentKey(key) {
			t.Errorf("commandExecutionEnvironmentKey(%q) = false", key)
		}
		if !commandExecutionEnvironmentAssignment(key + "=value") {
			t.Errorf("commandExecutionEnvironmentAssignment(%q) = false", key+"=value")
		}
	}

	functionVariable := "BASH_FUNC_pwn%%=() { ears-manager requirement add; }"
	if !assignmentWord.MatchString(functionVariable) {
		t.Fatal("assignmentWord did not recognize an exported Bash-function variable")
	}
	if !commandExecutionEnvironmentAssignment(functionVariable) {
		t.Fatal("exported Bash-function variable was not recognized as an execution override")
	}
	for _, assignment := range []string{"FOO=value", "BUILD_NUMBER=42", "GIT_COLOR=always", "BASH_FUNC_pwn=value"} {
		if commandExecutionEnvironmentAssignment(assignment) {
			t.Errorf("unrelated assignment %q was considered an execution override", assignment)
		}
	}

	for _, command := range []string{
		"GIT_ALLOW_PROTOCOL=ext git ls-remote origin",
		"GIT_PROXY_COMMAND=/tmp/git-proxy git ls-remote origin",
		"env GIT_ALLOW_PROTOCOL=ext git ls-remote origin",
		"env GIT_PROXY_COMMAND=/tmp/git-proxy git ls-remote origin",
		"timeout 5 env GIT_ALLOW_PROTOCOL=ext git ls-remote origin",
		"timeout 5 env GIT_PROXY_COMMAND=/tmp/git-proxy git ls-remote origin",
		"export GIT_ALLOW_PROTOCOL=ext",
		"export GIT_PROXY_COMMAND=/tmp/git-proxy",
	} {
		if result := invokesGovernedWrite(command); result != governedExecutionOpaque {
			t.Errorf("invokesGovernedWrite(%q) = %v, want opaque", command, result)
		}
	}
}

func TestGitInstawebIsOpaque(t *testing.T) {
	if !gitInvocationOpaque([]string{"git", "instaweb"}) {
		t.Fatal("git instaweb was considered a read-only command")
	}
}

func TestGitConfigConversionFlagsDoNotConsumeKeys(t *testing.T) {
	for _, option := range []string{"--bool", "--int", "--bool-or-int", "--path", "--expiry-date"} {
		t.Run(option, func(t *testing.T) {
			args := []string{option, "core.fsmonitor", "/tmp/evil"}
			state, safe := scanGitConfigWriteOptions(args)
			if !safe || len(state.positionals) != 2 || state.positionals[0] != "core.fsmonitor" || state.positionals[1] != "/tmp/evil" {
				t.Fatalf("opaque config scan = %+v, safe=%v; key was consumed", state, safe)
			}
			if !gitConfigCommandWritesExecutableConfig(args) {
				t.Fatal("config scan did not detect the executable key")
			}

			options, safe := parseGitConfigWriteOptions(args)
			if !safe || len(options.positionals) != 2 || options.positionals[0] != "core.fsmonitor" || options.positionals[1] != "/tmp/evil" {
				t.Fatalf("write-target config scan = %+v, safe=%v; key was consumed", options, safe)
			}
		})
	}
}

func TestGitConfigConversionFlagsRejectAttachedValues(t *testing.T) {
	for _, option := range []string{"--bool", "--int", "--bool-or-int", "--path", "--expiry-date"} {
		t.Run(option, func(t *testing.T) {
			args := []string{option + "=value", "core.fsmonitor", "/tmp/evil"}
			if _, safe := scanGitConfigWriteOptions(args); safe {
				t.Fatal("opaque config scan accepted an invalid attached value")
			}
			if _, safe := parseGitConfigWriteOptions(args); safe {
				t.Fatal("write-target config scan accepted an invalid attached value")
			}
		})
	}
}

func TestGitConfigTypeOptionConsumesAttachedAndSeparateValues(t *testing.T) {
	for _, args := range [][]string{
		{"--global", "--type=path", "user.name", "value"},
		{"--global", "--type", "path", "user.name", "value"},
	} {
		t.Run(args[1], func(t *testing.T) {
			state, safe := scanGitConfigWriteOptions(args)
			if !safe || len(state.positionals) != 2 || state.positionals[0] != "user.name" || state.positionals[1] != "value" {
				t.Fatalf("opaque config scan = %+v, safe=%v", state, safe)
			}
			if gitConfigCommandWritesExecutableConfig(args) {
				t.Fatal("harmless user.name config was considered executable")
			}

			options, safe := parseGitConfigWriteOptions(args)
			if !safe || len(options.positionals) != 2 || options.positionals[0] != "user.name" || options.positionals[1] != "value" {
				t.Fatalf("write-target config scan = %+v, safe=%v", options, safe)
			}
			command := append([]string{"git", "config"}, args...)
			if targets, safe := gitConfigWriteTargets(command, projectState{}, ""); !safe || len(targets) != 0 {
				t.Fatalf("global write targets = %v, safe=%v", targets, safe)
			}
		})
	}
}
