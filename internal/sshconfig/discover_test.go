package sshconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func put(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(result Result) []string {
	values := make([]string, len(result.Aliases))
	for i, alias := range result.Aliases {
		values[i] = alias.Name
	}
	return values
}

func TestDiscoverHostTokensAndSyntax(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, ".ssh", "config")
	put(t, path, `# comment
hOsT = alpha beta
Host *.example !blocked -option @bad " spaced " "кириллица"
HOST alpha
Host valid#comment
`)
	got, err := Discover(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "valid", "кириллица"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("aliases = %q, want %q", names(got), want)
	}
	if got.Aliases[0].Source != path || got.Aliases[0].Line != 2 {
		t.Fatalf("first alias location = %#v", got.Aliases[0])
	}
}

func TestDiscoverIncludeAnchoringGlobsAndState(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, "custom", "main.conf")
	put(t, main, "Include parts/*.conf\nHost main\n")
	put(t, filepath.Join(home, ".ssh", "parts", "b.conf"), "Host included-b\n")
	put(t, filepath.Join(home, ".ssh", "parts", "a.conf"), "Host included-a\nInclude nested.conf\n")
	put(t, filepath.Join(home, ".ssh", "nested.conf"), "Host nested\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "included-a,included-b,main,nested" {
		t.Fatalf("aliases = %q", names(got))
	}
	for _, alias := range got.Aliases {
		wantConditional := alias.Name == "included-b" || alias.Name == "nested"
		if alias.Conditional != wantConditional {
			t.Errorf("%s conditional = %v, want %v", alias.Name, alias.Conditional, wantConditional)
		}
	}
}

func TestDiscoverDefaultConfigAndConditional(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	config := filepath.Join(home, ".ssh", "config")
	put(t, config, "Match host *.corp\nInclude conditional.conf\nHost ordinary\n")
	put(t, filepath.Join(home, ".ssh", "conditional.conf"), "Host included-gated\n")
	got, err := Discover("")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != config {
		t.Fatalf("configPath = %q, want %q", got.ConfigPath, config)
	}
	for _, alias := range got.Aliases {
		wantConditional := alias.Name == "included-gated"
		if alias.Conditional != wantConditional {
			t.Errorf("%s conditional = %v, want %v", alias.Name, alias.Conditional, wantConditional)
		}
	}
	if len(got.Warnings) == 0 {
		t.Fatal("expected warning for Include in Match")
	}
}

func TestDiscoverCycleUnsupportedAndCommandDirectives(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	marker := filepath.Join(home, "should-not-exist")
	put(t, main, "Include loop.conf\nHost root\nProxyCommand touch "+marker+"\n")
	put(t, filepath.Join(home, ".ssh", "loop.conf"), "Include config\nHost nested\nLocalCommand touch "+marker+"\nInclude ~other/file\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command unexpectedly executed; marker stat err=%v", err)
	}
	if len(got.Warnings) < 3 {
		t.Fatalf("expected cycle, dynamic Include, and command warnings; got %v", got.Warnings)
	}
	for _, warning := range got.Warnings {
		if strings.Contains(warning, marker) || strings.Contains(warning, "touch") {
			t.Fatalf("warning leaked directive content: %q", warning)
		}
	}
}

func TestDiscoverIncludeQuotesAndSingleQuoteWarning(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	put(t, main, "Include \"folder with space/файл.conf\"\nInclude 'ignored.conf'\n")
	put(t, filepath.Join(home, ".ssh", "folder with space", "файл.conf"), "Host quoted\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "quoted" {
		t.Fatalf("aliases = %q", names(got))
	}
	if len(got.Warnings) == 0 || !strings.Contains(got.Warnings[0], "одинарные") {
		t.Fatalf("warnings = %v", got.Warnings)
	}
}

func TestDiscoverPreservesWindowsBackslashesAndEqualsInValues(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	included := filepath.Join(home, ".ssh", "folder=with", "file=one.conf")
	put(t, included, "Host alias-safe\n")
	put(t, main, "Include "+included+"\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "alias-safe" {
		t.Fatalf("aliases = %q", names(got))
	}
	if got.Aliases[0].Conditional {
		t.Fatal("Include beneath the implicit global block should stay unconditional")
	}
}

func TestDiscoverWhitespaceBeforeAssignmentSeparator(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	included := filepath.Join(home, ".ssh", "quoted folder", "file=one.conf")
	put(t, included, "Host included\n")
	put(t, main, "Include =\"quoted folder/file=one.conf\"\nHost =alpha beta\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "included"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("aliases = %q, want %q", names(got), want)
	}
}

func TestDiscoverRejectsCommandCharactersInAliases(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	file := filepath.Join(home, ".ssh", "config")
	put(t, file, "Host safe-name foo;bar bad^name bad%name bad=name\n")
	result, err := Discover(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(result), ",") != "safe-name" || len(result.Warnings) == 0 {
		t.Fatalf("unsafe discovery result: %+v", result)
	}
}

func TestDiscoverHostScopedIncludeIsConditional(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	put(t, main, "Host parent\nInclude child.conf\nHost plain\n")
	put(t, filepath.Join(home, ".ssh", "child.conf"), "Host child\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range got.Aliases {
		want := alias.Name == "child"
		if alias.Conditional != want {
			t.Errorf("%s conditional = %v, want %v", alias.Name, alias.Conditional, want)
		}
	}
}

func TestDiscoverWarnsForDirectMatchExecWithoutEchoingCommand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	put(t, main, "Match exec secret-command-argument\nHost conditional\n")
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) == 0 {
		t.Fatal("expected Match exec warning")
	}
	if len(got.Aliases) != 1 || got.Aliases[0].Conditional {
		t.Fatalf("top-level Host should begin an unconditional stanza: %#v", got.Aliases)
	}
	for _, warning := range got.Warnings {
		if strings.Contains(warning, "secret-command-argument") {
			t.Fatalf("warning leaked Match command: %q", warning)
		}
	}
}

func TestDiscoverCapsWarnings(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	main := filepath.Join(home, ".ssh", "config")
	put(t, main, strings.Repeat("Include %h/unsupported\n", maxWarnings*2))
	got, err := Discover(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != maxWarnings {
		t.Fatalf("warnings = %d, want at most %d", len(got.Warnings), maxWarnings)
	}
}

func TestDiscoverLimitsLinesAndMissingRoot(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, ".ssh", "config")
	put(t, path, "Host before\n"+strings.Repeat("x", maxLine+1)+"\nHost after\n")
	got, err := Discover(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "after,before" {
		t.Fatalf("aliases = %q", names(got))
	}
	if len(got.Warnings) == 0 {
		t.Fatal("expected oversized line warning")
	}
	if _, err := Discover(filepath.Join(home, ".ssh", "missing")); err == nil || !strings.Contains(err.Error(), "SSH-конфиг") {
		t.Fatalf("missing root error = %v", err)
	}
}
