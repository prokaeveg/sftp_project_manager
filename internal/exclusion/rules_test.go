package exclusion

import (
	"strings"
	"testing"
)

func TestMatchRecursiveGlobAndAnchoring(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{name: "double star can match zero segments", patterns: []string{"**/*.log"}, path: "build.log", want: true},
		{name: "double star matches nested file", patterns: []string{"**/*.log"}, path: "a/b/build.log", want: true},
		{name: "single star stays in one segment", patterns: []string{"*.log"}, path: "a/build.log", want: false},
		{name: "root anchored", patterns: []string{"docs/*.md"}, path: "other/docs/readme.md", want: false},
		{name: "root anchored match", patterns: []string{"docs/*.md"}, path: "docs/readme.md", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.patterns, tt.path); got != tt.want {
				t.Fatalf("Match(%q, %q) = %t, want %t", tt.patterns, tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchIncludesAncestors(t *testing.T) {
	tests := []struct {
		patterns []string
		path     string
	}{
		{patterns: []string{".git/**"}, path: ".git/config"},
		{patterns: []string{".git/**"}, path: ".git"},
		{patterns: []string{"*.log"}, path: "archive.log/child/file"},
	}
	for _, tt := range tests {
		if !Match(tt.patterns, tt.path) {
			t.Errorf("Match(%q, %q) = false, want true", tt.patterns, tt.path)
		}
	}
}

func TestMatchCaseInsensitiveAndUnicodeQuestion(t *testing.T) {
	if !Match([]string{"DOCS/?.TXT"}, "docs/Ж.txt") {
		t.Fatal("Match should compare case-insensitively and ? should match one Unicode rune")
	}
	if Match([]string{"docs/?.txt"}, "docs/аб.txt") {
		t.Fatal("? matched more than one Unicode rune")
	}
}

func TestValidateRejectsMalformedPatterns(t *testing.T) {
	invalid := []string{
		"", " \t", " leading", "trailing ", "/root", `dir\\file`, "C:/root",
		"folder/file:stream",
		"dir//file", "dir/", "./file", "../file", "ab**cd", "a/**b", "[ab]",
		"!important", "# comment", "line\nbreak", string([]byte{0xff}),
	}
	for _, pattern := range invalid {
		if err := Validate([]string{pattern}); err == nil {
			t.Errorf("Validate(%q) = nil, want error", pattern)
		}
	}
}

func TestValidateErrorsUseOneBasedRuleNumber(t *testing.T) {
	err := Validate([]string{"valid", "bad//pattern"})
	if err == nil || !strings.Contains(err.Error(), "правило 2") {
		t.Fatalf("Validate error = %v, want a Russian message naming rule 2", err)
	}
}

func TestValidateLimits(t *testing.T) {
	if err := Validate([]string{strings.Repeat("a", 512)}); err != nil {
		t.Fatalf("Validate at byte limit: %v", err)
	}
	if err := Validate([]string{strings.Repeat("a", 513)}); err == nil {
		t.Fatal("Validate accepted a pattern over the byte limit")
	}

	if err := Validate(make([]string, 100)); err == nil {
		t.Fatal("Validate accepted empty patterns")
	}
	patterns := make([]string, 100)
	for i := range patterns {
		patterns[i] = "*"
	}
	if err := Validate(patterns); err != nil {
		t.Fatalf("Validate at pattern-count limit: %v", err)
	}
	if err := Validate(append(patterns, "extra")); err == nil {
		t.Fatal("Validate accepted more than the pattern-count limit")
	}
}

func TestMatchInvalidInputDoesNotMatch(t *testing.T) {
	if Match([]string{"../secret"}, "safe/file") {
		t.Fatal("invalid pattern matched")
	}
	if Match([]string{"*"}, "../secret") {
		t.Fatal("invalid relative path matched")
	}
	if Match([]string{"**"}, ".") {
		t.Fatal("root path matched; Match requires a canonical non-root path")
	}
	if !Match([]string{"**"}, "child/file") {
		t.Fatal("** did not match a child path")
	}
}
