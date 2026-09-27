// Command feedme generates full-text RSS feeds for websites, including sites
// that publish no feed at all.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var version = "0.1.0"

// command is one subcommand.
type command struct {
	name    string
	summary string
	run     func(args []string) error
}

func commands() []command {
	return []command{
		{"probe", "fetch a URL and report what extraction found", runProbe},
		{"serve", "run the feed server", runServe},
		{"version", "print the version", runVersion},
		{"help", "show this help", runHelp},
	}
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runHelp(nil)
		os.Exit(2)
	}
	name := args[0]
	rest := args[1:]
	for _, c := range commands() {
		if c.name == name {
			if err := c.run(rest); err != nil {
				fmt.Fprintf(os.Stderr, "feedme %s: %v\n", name, err)
				os.Exit(1)
			}
			return
		}
	}
	// Allow a bare URL to mean "probe this", which is what you almost always
	// want to type.
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		if err := runProbe(args); err != nil {
			fmt.Fprintf(os.Stderr, "feedme: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "feedme: unknown command %q\n\n", name)
	runHelp(nil)
	os.Exit(2)
}

func runVersion(args []string) error {
	fmt.Printf("feedme %s\n", version)
	return nil
}

func runHelp(args []string) error {
	fmt.Printf(`feedme %s — full-text feed generator for sites without one

Usage:
  feedme <command> [flags] [args]
  feedme <url>              shorthand for: feedme probe <url>

Commands:
`, version)
	cmds := commands()
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].name < cmds[j].name })
	for _, c := range cmds {
		fmt.Printf("  %-10s %s\n", c.name, c.summary)
	}
	fmt.Print(`
Run "feedme <command> -h" for the flags of a specific command.
`)
	return nil
}

// flagSet is a tiny helper so each subcommand can declare flags without
// pulling in a CLI framework.
type flagSet struct {
	defs  map[string]*string
	help  map[string]string
	bools []boolFlag
}

// boolFlag records a boolean flag's wiring so Parse can set it after the
// string destination has been filled.
type boolFlag struct {
	name   string
	target *bool
	src    *string
}

func newFlags() *flagSet {
	return &flagSet{defs: map[string]*string{}, help: map[string]string{}}
}

// String declares a string flag, returning a pointer to its destination.
func (f *flagSet) String(name, def, usage string) *string {
	v := new(string)
	*v = def
	f.defs[name] = v
	f.help[name] = usage
	return v
}

// Bool declares a boolean flag; presence of the flag sets it true.
func (f *flagSet) Bool(name, usage string) *bool {
	v := new(string)
	f.defs[name] = v
	f.help[name] = usage
	b := new(bool)
	f.bools = append(f.bools, boolFlag{name: name, target: b, src: v})
	return b
}

// isBool reports whether name was declared as a boolean flag.
func (f *flagSet) isBool(name string) bool {
	for _, b := range f.bools {
		if b.name == name {
			return true
		}
	}
	return false
}

func (f *flagSet) Parse(args []string) ([]string, error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		var val string
		var hasVal bool
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, val, hasVal = name[:eq], name[eq+1:], true
		}
		if name == "h" || name == "help" {
			return rest, flagHelp{}
		}
		dst, ok := f.defs[name]
		if !ok {
			return rest, fmt.Errorf("unknown flag -%s", name)
		}
		if !hasVal {
			// Boolean flags are switches: they must not eat the positional
			// argument that follows them. Only `--flag=false` carries a value.
			if f.isBool(name) {
				val = "true"
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				val = args[i+1]
				i++
			}
		}
		*dst = val
	}
	for _, b := range f.bools {
		*b.target = !isFalsey(*b.src)
	}
	return rest, nil
}

// isFalsey interprets the textual value of a boolean flag.
func isFalsey(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "false", "0", "no", "off":
		return true
	}
	return false
}

// flagHelp signals that help was requested rather than a parse failure.
type flagHelp struct{}

func (flagHelp) Error() string { return "help requested" }

func (f *flagSet) usage() string {
	var b strings.Builder
	flags := make([]string, 0, len(f.defs))
	for k := range f.defs {
		flags = append(flags, k)
	}
	sort.Strings(flags)
	for _, k := range flags {
		fmt.Fprintf(&b, "  -%-16s %s\n", k, f.help[k])
	}
	return b.String()
}
