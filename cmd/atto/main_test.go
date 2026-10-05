package main

import "testing"

func TestUnknownCommand(t *testing.T) {
	known := subcommandNames()
	for _, c := range []struct {
		args []string
		msg  string
		ok   bool
	}{
		{[]string{"upgrade"}, `atto: unknown command "upgrade". Did you mean "update"?`, true},
		{[]string{"histroy"}, `atto: unknown command "histroy". Did you mean "history"?`, true},
		{[]string{"xyz"}, `atto: unknown command "xyz" (see atto -h)`, true},
		{[]string{"_supervis"}, `atto: unknown command "_supervis" (see atto -h)`, true}, // hidden never suggested
		{[]string{"fix the build"}, "", false},
		{[]string{"fix", "the", "build"}, "", false},
		{[]string{"fix", "build"}, "", false},
		{nil, "", false},
	} {
		msg, ok := unknownCommand(c.args, known)
		if msg != c.msg || ok != c.ok {
			t.Errorf("%q: got %q, %v", c.args, msg, ok)
		}
	}
}

func TestInitialPrompt(t *testing.T) {
	for _, args := range [][]string{{"fix the build"}, {"fix", "the", "build"}} {
		if got := initialPrompt(args); got != "fix the build" {
			t.Errorf("%q -> %q", args, got)
		}
	}
	if initialPrompt(nil) != "" {
		t.Error("no words, no prompt")
	}
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		d    int
	}{{"", "abc", 3}, {"abc", "abc", 0}, {"kitten", "sitting", 3}} {
		if got := editDistance(c.a, c.b); got != c.d {
			t.Errorf("%s/%s = %d", c.a, c.b, got)
		}
	}
}
