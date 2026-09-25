# herald

Send the webhook a provider would have sent, signed the way that provider
signs it, to a URL you choose.

```
herald send stripe payment_intent.succeeded --to http://localhost:8000/stripe
```

The signature verifies. That is the whole point: your handler runs its real
verification code against a real signature, and rejects the one you asked
herald to break.

Run `herald` with no arguments for the browser.

```
╭──────────────────────────────────╮ ╭──────────────────────────────────╮
│ providers                        │ │ events                           │
│ > stripe                         │ │ > charge.refunded                │
│   mollie                         │ │   checkout.session.completed     │
│   github                         │ │   payment_intent.succeeded       │
╰──────────────────────────────────╯ ╰──────────────────────────────────╯
target  http://localhost:8000/stripe
secret  whsec_test_secret  the pack's example

enter send   d duplicate   b bad sig   r replay 6m   x burst 10   t target
```

## Why not the provider's own CLI

For one provider, use theirs. `stripe trigger` creates real test-mode objects,
so its payload is better than any fixture can be.

herald is for the other cases. You are testing eleven providers, or you are
offline, or you are sending to a staging URL, or the provider has no CLI, or
you want the same interface for all of them. And it runs locally, so the
signing secret for your endpoint stays on your machine.

## Testing delivery, not just payloads

One well-formed delivery to a handler proves the happy path, and the happy path
is not where webhook handlers fail. They fail on the second copy of something
they already processed, on an event that arrives before the one it depends on,
and on a signature that is correct but too old.

```bash
# A provider retry: the same delivery id, twice. Not idempotent? You will see.
herald send stripe payment_intent.succeeded --to $URL --duplicate --count 2

# Out of order, because nobody guarantees order.
herald send stripe charge.refunded payment_intent.succeeded --to $URL

# Fifty at once, straight at your connection pool.
herald send github push --to $URL --count 50

# Correctly signed, six minutes old. Stripe documents five.
herald send stripe payment_intent.succeeded --to $URL --skew -6m

# A signature that is wrong but well formed. A 500 here rather than a 401
# means the handler is failing to parse, not failing to verify.
herald send shopify orders.create --to $URL --bad-signature

# Build it, sign it, print it, send nothing.
herald send stripe payment_intent.succeeded --to $URL --dry-run
```

## Providers

Eleven packs ship in the binary: `stripe`, `mollie`, `mollie-classic`,
`github`, `shopify`, `slack`, `twilio`, `paddle`, `square`,
`standardwebhooks` and `paypal`.

`standardwebhooks` covers everything that adopted the spec, which is most of
what sends through Svix: Clerk, Resend and others. If your endpoint reads
`webhook-id` and `webhook-signature`, that is the pack.

Two of them will not do what you expect, and say so before they send:

- **`mollie-classic`** is unsigned and carries no payload. The body is
  `id=tr_...` and Mollie expects your application to call the API and read the
  status back. Sending it makes your application call the real Mollie API with
  an id that does not exist in your account, so it covers the route and not the
  logic.
- **`paypal`** signs with RSA against a certificate your handler downloads from
  a paypal.com URL. There is no shared secret, so nothing here can produce a
  delivery that verifies. The payload is real and the signature is absent. A
  handler that accepts it is telling you it never verifies anything.

## Packs

A provider is a directory, not code:

```
providers/stripe/
├── provider.yaml
└── events/
    ├── payment_intent.succeeded.json
    └── charge.refunded.json
```

`provider.yaml` writes the signature down instead of implementing it:

```yaml
name: Stripe
docs: https://docs.stripe.com/webhooks/signatures

timestamp:
  format: unix
  tolerance: 5m

signing:
  - header: Stripe-Signature
    algorithm: sha256
    encoding: hex
    payload: "{timestamp}.{body}"
    format: "t={timestamp},v1={signature}"
```

Placeholders are `{body}`, `{timestamp}`, `{id}`, `{event}`, `{url}`,
`{path}`, `{host}` and `{method}`, plus `{signature}` in `format`. An event is
a file in `events/`, named for the event, and the extension sets the content
type. Adding an event to a pack is adding one file.

`{url}` is there because Twilio and Square sign the target URL, which is why
signing takes the whole request rather than the body. Twilio needs more than a
template and is the only provider implemented in Go.

Adding a provider is a pull request against `providers/`. Everything a pack
claims is checked in CI, including against a second implementation of the
signing rules written in Python, so a pack the Go engine reads differently
fails there rather than in somebody's afternoon.

## Where packs come from

`herald sync` pulls the current set. Three layers, later wins:

| Layer | Where | What it is |
|---|---|---|
| embedded | in the binary | the snapshot at release, so a fresh install works offline |
| synced | `$XDG_DATA_HOME/herald/providers` | what `herald sync` downloaded. Safe to delete |
| local | `$XDG_CONFIG_HOME/herald/providers` | yours. Sync never touches it |

The split matters if you keep work in there. A pack you wrote, or a private
provider that is not going public, goes in the config directory and survives
every sync. Point `HERALD_PACKS_REPO` at a fork to ship packs to your own team.

## Secrets

In order: `--secret`, then `HERALD_SECRET_STRIPE`, then `HERALD_SECRET`, then
the example in the pack. Set your application to the pack's example and
everything just works locally.

herald does not save secrets. The browser remembers your target URL per
provider and nothing else, because a config file full of signing secrets is a
worse thing to own than a paste.

## Building

```bash
go build -o bin/herald ./cmd/herald
```

## Honest about the fixtures

The payloads that ship today were written from each provider's documentation,
not captured from a live delivery. They are the right shape and the right
fields; they are not byte-identical to production traffic. Captured events are
better and replacing one is a pull request.

Signatures are a different matter, and are checked against GitHub's and
Standard Webhooks' own published test vectors.
