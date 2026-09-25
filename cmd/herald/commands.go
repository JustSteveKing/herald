package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"golang.org/x/term"

	"github.com/JustSteveKing/herald/deliver"
	"github.com/JustSteveKing/herald/internal/sync"
	"github.com/JustSteveKing/herald/internal/tui"
	"github.com/JustSteveKing/herald/pack"
)

// version is set at build time by the release workflow.
var version = "dev"

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "herald",
		Short: "Send fake, correctly signed provider webhooks to any URL",
		Long: `herald sends the webhook a provider would have sent, signed the way that
provider signs it, to a URL you choose.

Everything it knows about a provider lives in a pack: a directory holding the
payloads and the signing rules. Packs ship with the binary and update on their
own schedule with "herald sync".`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		Args:          cobra.NoArgs,
		// No arguments starts the browser, because the common case is picking
		// something rather than knowing its name. Piped or redirected, it
		// prints help instead: a full screen interface written to a file
		// helps nobody.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return cmd.Help()
			}

			set, err := load()
			if err != nil {
				return err
			}

			return tui.Run(set)
		},
	}

	cmd.AddCommand(providersCmd(), eventsCmd(), showCmd(), sendCmd(), syncCmd())

	return cmd
}

func load() (*pack.Set, error) {
	set, err := pack.Default()
	if err != nil {
		return nil, err
	}
	if set.Len() == 0 {
		return nil, fmt.Errorf("no providers found, which should be impossible: try herald sync")
	}
	return set, nil
}

func providersCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "providers",
		Aliases: []string{"list"},
		Short:   "List the providers herald can send as",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, err := load()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%-18s %-22s %7s  %-9s %s\n", "PROVIDER", "NAME", "EVENTS", "SOURCE", "SIGNING")

			for _, p := range set.All() {
				fixtures, err := p.Fixtures()
				if err != nil {
					return err
				}

				fmt.Fprintf(out, "%-18s %-22s %7d  %-9s %s\n",
					p.ID, p.Name, len(fixtures), p.Source(), describeSigning(p))
			}

			if entries := set.Incompatibles(); len(entries) > 0 {
				// Sized to the content, because these names run long and a
				// fixed column turns the section into a ragged mess.
				width := 0
				for _, e := range entries {
					width = max(width, len(e.Name))
				}

				fmt.Fprintf(out, "\nNot shipped, because nothing outside the provider can sign one:\n")
				for _, e := range entries {
					fmt.Fprintf(out, "%-18s %-*s  %s\n", e.ID, width, e.Name, e.Scheme)
				}
				fmt.Fprintf(out, "Naming one tells you what to use instead.\n")
			}

			if manifest, ok := sync.ReadManifest(pack.SyncDir()); ok {
				fmt.Fprintf(out, "\nSynced from %s@%s on %s\n",
					manifest.Repo, manifest.Ref, manifest.FetchedAt.Local().Format("2006-01-02 15:04"))
			} else {
				fmt.Fprintf(out, "\nNothing synced yet, so these are the packs built into this binary.\nRun herald sync to pull the current set.\n")
			}

			return nil
		},
	}
}

func describeSigning(p pack.Provider) string {
	if len(p.Signing) == 0 {
		return "none, this provider does not sign"
	}

	parts := make([]string, 0, len(p.Signing))
	for _, sig := range p.Signing {
		parts = append(parts, sig.Header)
	}

	return strings.Join(parts, ", ")
}

func eventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events <provider>",
		Short: "List the payloads a provider pack carries",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := load()
			if err != nil {
				return err
			}

			p, err := provider(set, args[0])
			if err != nil {
				return err
			}

			fixtures, err := p.Fixtures()
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if p.Notes != "" {
				fmt.Fprintln(out, indent(strings.TrimSpace(p.Notes), "  "))
				fmt.Fprintln(out)
			}

			for _, f := range fixtures {
				fmt.Fprintf(out, "%-34s %s\n", f.ID, f.Meta.Description)
			}

			return nil
		},
	}
}

func showCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <provider> <event>",
		Short: "Print the payload herald would send",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := load()
			if err != nil {
				return err
			}

			p, err := provider(set, args[0])
			if err != nil {
				return err
			}

			f, err := p.Fixture(args[1])
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), string(f.Body))
			return nil
		},
	}
}

func sendCmd() *cobra.Command {
	var (
		target   string
		secret   string
		count    int
		dup      bool
		interval time.Duration
		skew     time.Duration
		bad      bool
		headers  []string
		timeout  time.Duration
		dryRun   bool
		showBody bool
	)

	cmd := &cobra.Command{
		Use:   "send <provider> <event> [event...]",
		Short: "Send one or more signed deliveries",
		Long: `Send the named events, in the order given.

Giving several events is how you test ordering: providers do not guarantee it,
and a handler that assumes created arrives before updated will be wrong in
production long before it is wrong often enough to notice.`,
		Example: `  herald send stripe payment_intent.succeeded --to http://localhost:8000/stripe
  herald send stripe charge.refunded payment_intent.succeeded --to $URL
  herald send github push --to $URL --count 50 --interval 0
  herald send stripe payment_intent.succeeded --to $URL --duplicate --count 2
  herald send slack url_verification --to $URL --skew -6m
  herald send shopify orders.create --to $URL --bad-signature`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			set, err := load()
			if err != nil {
				return err
			}

			p, err := provider(set, args[0])
			if err != nil {
				return err
			}

			parsed, err := parseHeaders(headers)
			if err != nil {
				return err
			}

			opts := deliver.Options{
				Target:    target,
				Secret:    resolveSecret(p, secret),
				Count:     count,
				Duplicate: dup,
				Interval:  interval,
				Skew:      skew,
				Corrupt:   bad,
				Headers:   parsed,
				Timeout:   timeout,
				DryRun:    dryRun,
			}

			out := cmd.OutOrStdout()
			warn(out, p, opts)

			for _, event := range args[1:] {
				f, err := p.Fixture(event)
				if err != nil {
					return err
				}

				results, err := deliver.Send(context.Background(), p, f, opts)
				if err != nil {
					return err
				}

				report(out, p, f, opts, results, showBody)
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&target, "to", os.Getenv("HERALD_TARGET"), "URL to deliver to, or $HERALD_TARGET")
	flags.StringVar(&secret, "secret", "", "signing secret, or $HERALD_SECRET, defaulting to the pack's example")
	flags.IntVar(&count, "count", 1, "how many times to send")
	flags.BoolVar(&dup, "duplicate", false, "reuse one delivery id for every send, as a provider retry does")
	flags.DurationVar(&interval, "interval", 0, "wait between sends, zero for a burst")
	flags.DurationVar(&skew, "skew", 0, "offset the timestamp, negative to age the delivery")
	flags.BoolVar(&bad, "bad-signature", false, "break the signature while leaving the header well formed")
	flags.StringArrayVar(&headers, "header", nil, "extra header as name=value, repeatable")
	flags.DurationVar(&timeout, "timeout", 10*time.Second, "per-request timeout")
	flags.BoolVar(&dryRun, "dry-run", false, "build and sign the request, print it, send nothing")
	flags.BoolVar(&showBody, "response", false, "print what the endpoint answered, not only its status")

	_ = cmd.MarkFlagRequired("to")

	return cmd
}

func syncCmd() *cobra.Command {
	var (
		repo string
		ref  string
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Pull the current provider packs",
		Long: `Download the provider packs and replace the synced copy.

Packs land in the XDG data directory, because they are a cache of somebody
else's repository and can be deleted at any time. Anything you write in the
XDG config directory is never touched by this.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := pack.SyncDir()
			if dir == "" {
				return fmt.Errorf("cannot work out where to put packs: set HERALD_SYNC_DIR")
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Fetching %s@%s\n", cmp(repo, sync.DefaultRepo), cmp(ref, sync.DefaultRef))

			manifest, err := sync.Run(context.Background(), sync.Options{
				Repo: repo,
				Ref:  ref,
				Dir:  dir,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "%d providers at %s\n%s\n", manifest.Providers, short(manifest.Commit), dir)
			return nil
		},
	}

	cmd.Flags().StringVar(&repo, "repo", "", "owner/name to pull packs from, or $HERALD_PACKS_REPO")
	cmd.Flags().StringVar(&ref, "ref", "", "branch or tag to pull")

	return cmd
}
