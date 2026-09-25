package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/JustSteveKing/herald/deliver"
	"github.com/JustSteveKing/herald/pack"
)

func provider(set *pack.Set, id string) (pack.Provider, error) {
	if p, ok := set.Provider(id); ok {
		return p, nil
	}

	// Absent on purpose is a different answer from absent, and the person
	// asking has usually already spent a while wondering which it was.
	if entry, ok := set.Incompatible(id); ok {
		return pack.Provider{}, incompatibleError(entry)
	}

	names := make([]string, 0, set.Len())
	for _, p := range set.All() {
		names = append(names, p.ID)
	}
	sort.Strings(names)

	return pack.Provider{}, fmt.Errorf("no provider %q. Known: %s", id, strings.Join(names, ", "))
}

// incompatibleError explains rather than refuses. A pack for one of these
// could be written in an afternoon and would send the payload with no valid
// signature, which passes against a handler that never verifies anything. The
// error says that, and then says what to do instead, because a tool that only
// tells you what it will not do is not much help.
func incompatibleError(e pack.Incompatible) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s cannot be faked, so herald does not ship a pack for it.\n\n", e.Name)
	fmt.Fprintf(&b, "  %s\n\n", e.Summary)
	fmt.Fprintf(&b, "  Shipping the payload with no valid signature would pass against a handler\n")
	fmt.Fprintf(&b, "  that never verifies anything, which is the one worth catching.\n\n")

	if e.Workaround != "" {
		fmt.Fprintf(&b, "Instead:\n%s\n", indent(strings.TrimSpace(e.Workaround), "  "))
	}
	if e.Docs != "" {
		fmt.Fprintf(&b, "\n  %s", e.Docs)
	}

	return errors.New(b.String())
}

// resolveSecret prefers the flag, then an environment variable for this
// provider, then a general one, then the pack's example.
//
// Falling back to the example is deliberate. The common case is testing an
// application you also configured, so setting both ends to the documented
// example secret is the fastest way to a delivery that verifies, and herald
// says which secret it used so nobody is surprised later.
func resolveSecret(p pack.Provider, flag string) string {
	if flag != "" {
		return flag
	}

	specific := "HERALD_SECRET_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(p.ID))
	if value := os.Getenv(specific); value != "" {
		return value
	}
	if value := os.Getenv("HERALD_SECRET"); value != "" {
		return value
	}

	return p.Secret.Example
}

func parseHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(raw))
	for _, entry := range raw {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("header %q is not name=value", entry)
		}
		out[strings.TrimSpace(name)] = value
	}

	return out, nil
}

// warn says the things that would otherwise turn a green result into a false
// one. It runs before the first delivery, not after, because the point is to
// stop somebody concluding their handler is fine.
func warn(out io.Writer, p pack.Provider, opts deliver.Options) {
	if p.Notes != "" {
		fmt.Fprintf(out, "%s\n\n", indent(strings.TrimSpace(p.Notes), "! "))
	}

	if opts.Skew != 0 && p.Timestamp.Tolerance > 0 {
		if -opts.Skew > p.Timestamp.Tolerance {
			fmt.Fprintf(out, "! The timestamp is %s old and %s documents a %s tolerance,\n!  so %s itself would not have sent this. A handler that accepts it\n!  is not checking the replay window.\n\n",
				(-opts.Skew).String(), p.Name, p.Timestamp.Tolerance, p.Name)
		}
	}

	if opts.Corrupt && len(p.Signing) == 0 {
		fmt.Fprintf(out, "! %s does not sign its webhooks, so there is no signature to break.\n\n", p.Name)
	}
}

func report(out io.Writer, p pack.Provider, f pack.Fixture, opts deliver.Options, results []deliver.Result, showBody bool) {
	for _, r := range results {
		if opts.DryRun {
			printRequest(out, r)
			continue
		}

		status := fmt.Sprintf("%d", r.Status)
		mark := "ok "
		if r.Err != nil {
			status, mark = "---", "err"
		} else if !r.OK() {
			mark = "bad"
		}

		fmt.Fprintf(out, "%s %-18s %-30s %4s  %8s  %s\n",
			mark, p.ID, f.ID, status, r.Duration.Round(time.Millisecond), r.DeliveryID)

		switch {
		case r.Err != nil:
			fmt.Fprintf(out, "    %v\n", r.Err)
		case showBody && r.Body != "":
			// Whole body, not the first line, because the caller asked and
			// because an echo service answering with the request it received
			// is the only way to check what actually arrived.
			fmt.Fprintln(out, r.Body)
		case !r.OK() && r.Body != "":
			fmt.Fprintf(out, "    %s\n", firstLine(r.Body))
		}
	}
}

// printRequest shows the exact delivery without making it. Nothing herald
// sends is destructive on its own, but the endpoint on the other end might be,
// and reading the request is cheaper than reading a log afterwards.
func printRequest(out io.Writer, r deliver.Result) {
	fmt.Fprintf(out, "%s %s\n", r.Request.Method, r.Request.URL)

	names := make([]string, 0, len(r.Request.Header))
	for name := range r.Request.Header {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Fprintf(out, "%s: %s\n", name, r.Request.Header.Get(name))
	}

	fmt.Fprintf(out, "\n%s\n\n", string(r.Request.Body))
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func cmp(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}
