package main

import "testing"

// A boolean switch must not consume the positional argument after it. It used
// to, so `feedme probe --json https://example.com` swallowed the URL and ran
// with no input at all.
func TestBoolFlagDoesNotEatPositional(t *testing.T) {
	fs := newFlags()
	asJSON := fs.Bool("json", "emit JSON")
	ua := fs.String("user-agent", "", "ua")

	rest, err := fs.Parse([]string{"--json", "https://example.com/a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != "https://example.com/a" {
		t.Fatalf("positional args = %v, want the URL intact", rest)
	}
	if !*asJSON {
		t.Error("--json should be true")
	}
	if *ua != "" {
		t.Errorf("--user-agent should stay empty, got %q", *ua)
	}
}

func TestBoolFlagAcceptsExplicitValue(t *testing.T) {
	for _, in := range []string{"true", "1", "yes", ""} {
		fs := newFlags()
		b := fs.Bool("json", "")
		if _, err := fs.Parse([]string{"--json=" + in}); err != nil {
			t.Fatal(err)
		}
		want := in != ""
		if *b != want {
			t.Errorf("--json=%q set %v, want %v", in, *b, want)
		}
	}
	for _, in := range []string{"false", "0", "no"} {
		fs := newFlags()
		b := fs.Bool("json", "")
		if _, err := fs.Parse([]string{"--json=" + in}); err != nil {
			t.Fatal(err)
		}
		if *b {
			t.Errorf("--json=%s should be false", in)
		}
	}
}

func TestStringFlagConsumesNextArg(t *testing.T) {
	fs := newFlags()
	timeout := fs.String("timeout", "20s", "")
	rest, err := fs.Parse([]string{"--timeout", "45s", "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if *timeout != "45s" {
		t.Errorf("timeout = %q, want 45s", *timeout)
	}
	if len(rest) != 1 {
		t.Errorf("positional args = %v", rest)
	}
}

func TestMixedFlagsAndPositionals(t *testing.T) {
	fs := newFlags()
	fs.Bool("html", "")
	fs.Bool("no-robots", "")
	ua := fs.String("user-agent", "default", "")

	rest, err := fs.Parse([]string{
		"https://a.example/1",
		"--no-robots",
		"--user-agent", "MyBot/1.0",
		"--html",
		"https://b.example/2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if *ua != "MyBot/1.0" {
		t.Errorf("user-agent = %q", *ua)
	}
	want := []string{"https://a.example/1", "https://b.example/2"}
	if len(rest) != len(want) {
		t.Fatalf("positionals = %v, want %v", rest, want)
	}
	for i := range want {
		if rest[i] != want[i] {
			t.Errorf("positionals = %v, want %v", rest, want)
			break
		}
	}
}

func TestSingleDashForm(t *testing.T) {
	fs := newFlags()
	asJSON := fs.Bool("json", "")
	rest, err := fs.Parse([]string{"-json", "https://e.example"})
	if err != nil {
		t.Fatal(err)
	}
	if !*asJSON || len(rest) != 1 {
		t.Errorf("single-dash form failed: json=%v rest=%v", *asJSON, rest)
	}
}

func TestUnknownFlagIsAnError(t *testing.T) {
	fs := newFlags()
	fs.Bool("json", "")
	if _, err := fs.Parse([]string{"--nope"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
}

func TestHelpIsRequestedNotAnError(t *testing.T) {
	fs := newFlags()
	fs.Bool("json", "")
	for _, in := range []string{"-h", "--help", "-help"} {
		_, err := fs.Parse([]string{in})
		if _, ok := err.(flagHelp); !ok {
			t.Errorf("%s should return flagHelp, got %v", in, err)
		}
	}
}

// "GET", "post:" and paths with dashes must not be mistaken for flags.
func TestPositionalLooksLikeFlagEdgeCases(t *testing.T) {
	fs := newFlags()
	rest, err := fs.Parse([]string{"/tmp/page.html", "https://e.example/a-b-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 {
		t.Errorf("positionals = %v, want both kept", rest)
	}
}
