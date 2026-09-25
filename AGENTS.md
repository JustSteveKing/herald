# AGENTS.md

Instructions for an agent working in this repository. Humans want `README.md`.

## The one rule

herald makes exactly one claim: **the signature verifies**. Everything else is
convenience. A change that makes herald produce a signature a real provider
would not produce is a bug even when every test passes, and a provider whose
signature cannot be produced correctly does not ship at all.

Two consequences you will hit:

- **Never reformat a payload.** Every scheme here signs the raw bytes. Pretty
  printing a fixture on the way out, re-encoding JSON, or trimming whitespace
  invalidates a signature the handler is about to check.
- **Never add a pack that sends an unsigned payload for a provider that
  signs.** It passes against a handler that never verifies anything, which is
  the handler most worth catching, and reports that as success. See
  `providers/incompatible.yaml`.

## Never write a signing scheme from memory

This is the mistake that has already been made here twice.

Read the provider's documentation. If the documentation describes the scheme
in prose rather than pseudocode, **read their official SDK** and follow what
the code does. Put the source in the pack's `docs` field.

Two examples already in the tree, both of which a from-memory implementation
gets wrong:

- **Twilio** sorts repeated form parameters and deduplicates their values,
  repeating the key before each one. Joining them in arrival order produces a
  signature that looks fine. Only `twilio-python`'s `RequestValidator` says so.
- **Square** signs `notificationUrl + body`. Their docs name the three inputs
  and never say how they are joined. `WebhooksHelper.ts` does.

## Layout

```
pack/         reads provider packs. Data types and loading, no behaviour
signer/       turns a declared rule into headers. builtin.go is the exceptions
deliver/      builds, signs and sends. The delivery controls live here
providers/    the packs themselves, plus incompatible.yaml
cmd/herald/   cobra commands and output formatting
internal/tui  the bubbletea browser
internal/sync fetching packs from GitHub
scripts/      the vector generator, the README generator, the smoke test
```

`pack` holds data and `signer` holds behaviour, the same split as `spec` and
`engine` in the sibling project `legible`. Keep it. `pack` must not import
`signer`.

## Adding a provider

Nine of ten providers need no Go. Reach for code only when the scheme cannot be
written down.

1. `providers/<id>/provider.yaml`, where `<id>` is the directory name. The
   loader refuses a pack whose `id` field disagrees with its directory.
2. `providers/<id>/events/<event>.json`. The extension sets the content type:
   `.json`, `.xml`, `.form` for `application/x-www-form-urlencoded`, `.txt`.
   The event id is the filename without the extension.
3. Fill `docs` with the page you took the scheme from. This is not optional and
   a test enforces it.
4. Give `secret.example` a value. Signing providers need one or nothing can be
   sent without `--secret`.
5. Regenerate and test:

```bash
python3 scripts/gen_vectors.py > signer/vectors_test.go
go test ./...
```

Placeholders available in `payload`, `format` and any transport header:
`{body}`, `{timestamp}`, `{id}`, `{event}`, `{url}`, `{path}`, `{host}`,
`{method}`, and `{signature}` in `format` only.

Things that are easy to get wrong:

- **Secret handling differs between providers that look identical.** Stripe
  keys the HMAC with the whole `whsec_...` string as printed. Standard Webhooks
  strips the prefix and base64 decodes what is left. Use `secret.prefix` and
  `secret.encoding`.
- **`encoding` is `hex` or `base64`** and providers disagree. Shopify is
  base64, GitHub is hex.
- **A provider can need more than one signature header.** `signing` is a list.
  GitHub still sends its SHA-1 header alongside the SHA-256 one.
- **Do not declare a header with an empty value.** It travels the wire intact
  and means nothing, and it invites a handler to branch on its presence.
- **Event metadata keys must match a real fixture.** A typo in the `events:`
  map silently drops a header override. A test catches this.

## Before you say it is done

```bash
gofmt -l .                                   # must print nothing
go vet ./...
go test ./...
python3 scripts/gen_vectors.py > signer/vectors_test.go && git diff --exit-code signer/vectors_test.go
python3 scripts/gen_readme.py && git diff --exit-code README.md
```

Both generated files are checked in CI and fail the build on a diff. The
vectors are a second implementation of the pack rules in Python; if they
disagree with the Go engine, work out which one is wrong rather than
regenerating until it is quiet.

`scripts/smoke.sh` sends every fixture over a real network to an echo service.
Run it when you have touched `deliver`, `signer` or a pack's transport. It is
not in CI, it reaches the internet, and it is the only check that sees what
actually arrives.

## Do not

- **Do not add a dependency** without saying why in the commit. This is a
  single static binary with no cgo, and `curl | sh` distribution depends on
  staying that way.
- **Do not bend a scheme into the declarative engine** if it does not fit. Add
  a builtin in `signer/builtin.go` and keep that map small. Every entry in it
  is a provider that can only be added by releasing a new binary, which is the
  thing packs exist to avoid.
- **Do not persist secrets.** Targets are remembered, secrets never are. There
  is a test that fails if one reaches disk.
- **Do not invent fixture data and present it as captured.** Fixtures today are
  written from documentation and the README says so. If you capture a real
  delivery, say which provider and when.
- **Do not make the smoke test part of CI.** It depends on somebody else's free
  service.

## Style

Match what is there. Comments explain **why**, not what: the reasoning that
would otherwise be lost, the trap already paid for, the alternative that was
rejected. A comment restating the line below it is noise.

Prose in this repository, including the README and commit messages, uses
British spelling and no em dashes. Commit subjects are lowercase and say what
changed, not what you did: `signer: take the whole request, not the body`.
