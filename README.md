# herald

Send a webhook that verifies.

herald sends the request a provider would have sent, signed the way that
provider signs it, to any URL you name. Your handler runs its real
verification code against a real signature, and rejects the one you asked
herald to break.

```console
$ herald send stripe payment_intent.succeeded --to http://localhost:8000/stripe
ok  stripe             payment_intent.succeeded        200      31ms  bbe17bfe-a504-48ac-a380-6f37ebc9da9d

$ herald send stripe payment_intent.succeeded --to http://localhost:8000/stripe --bad-signature
bad stripe             payment_intent.succeeded        401       8ms  4d087811-9ed0-4f6c-bbe6-9b8d0b2e3a52
    Invalid signature
```

One binary, no service and no account. Ten providers ship inside it, so it
works before it has ever reached the network, and your signing secret never
leaves the machine.

## Install

**macOS and Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/JustSteveKing/herald/main/install.sh | sh
```

It downloads the archive for your platform, checks it against the release's
`checksums.txt`, and installs to `~/.local/bin` when that is on your PATH and
`/usr/local/bin` otherwise. `--bin-dir` overrides it, `--version vX.Y.Z` pins a
release, and `--help` lists the rest.

**From source**, which is what the same script does when you run it inside a
checkout:

```bash
git clone https://github.com/JustSteveKing/herald.git
cd herald && ./install.sh
```

A source build stamps the version with `git describe`, so `herald --version`
printing a commit means you built it and printing `v0.1.1` means you downloaded
it. Worth having the moment you wonder why a fix is not in your binary.

## Use

Run `herald` with no arguments and pick something.

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

It remembers the target per provider, because a Stripe endpoint and a GitHub
endpoint are different routes in the same application and swapping between them
is the whole motion.

Everything the browser does has a flag, so the same run goes in a script:

```bash
herald providers                      # what can I send as
herald events stripe                  # what can I send
herald show stripe charge.refunded    # what is in it
```

### Delivery, not just payloads

One well-formed delivery proves the happy path, and the happy path is not where
webhook handlers fail. They fail on the second copy of something they already
processed, on an event that arrives before the one it depends on, and on a
signature that is correct but too old.

```bash
# A provider retry: one delivery id, sent twice. Not idempotent? You will see.
herald send stripe payment_intent.succeeded --to $URL --duplicate --count 2

# Out of order, because nobody guarantees order.
herald send stripe charge.refunded payment_intent.succeeded --to $URL

# Fifty at once, straight at your connection pool.
herald send github push --to $URL --count 50

# Correctly signed, six minutes old. Stripe documents a five minute tolerance.
herald send stripe payment_intent.succeeded --to $URL --skew -6m

# Wrong signature, well formed header. A 500 here rather than a 401 means the
# handler is failing to parse rather than failing to verify.
herald send shopify orders.create --to $URL --bad-signature

# Build it, sign it, print it, send nothing.
herald send stripe payment_intent.succeeded --to $URL --dry-run
```

## Providers

Ten packs ship in the binary: `stripe`, `mollie`, `mollie-classic`, `github`,
`shopify`, `slack`, `twilio`, `paddle`, `square` and `standardwebhooks`.

`standardwebhooks` covers everything that adopted the spec, which is most of
what sends through Svix: Clerk, Resend and others. If your endpoint reads
`webhook-id` and `webhook-signature`, that is your pack whatever the vendor is
called.

Two behave differently enough to say so before they send.

**`mollie-classic`** is unsigned and carries no payload. The body is
`id=tr_...`, and Mollie expects your application to call the API and read the
status back. Sending it makes your application call the real Mollie API with an
id that is not in your account, so it covers the route and not the logic.

**`twilio`** signs the target URL along with the form parameters, so the
signature is only valid for the exact `--to` you gave. That is not a quirk to
work around: it is the most common cause of Twilio signature failures in
production, where a load balancer terminates TLS and the application sees
`http` on a URL Twilio signed as `https`.

### Providers herald will not ship

If verifying a delivery means fetching a public key or a certificate from a
domain the provider controls, there is no shared secret and nothing outside
that provider can produce a delivery that verifies.

A pack that sent the payload with no valid signature would be worse than no
pack. It passes against a handler that never verifies anything, which is
exactly the handler worth catching, and it reports that as a green result. So
these are absent on purpose, and naming one tells you what to use instead:

```console
$ herald send paypal PAYMENT.CAPTURE.COMPLETED --to $URL
herald: PayPal cannot be faked, so herald does not ship a pack for it.
```

Which is not the same as a provider that does not sign at all. Mollie's classic
webhook has no signature by design, so herald reproduces it exactly and ships
it. Absent by design is reproducible; unforgeable is not.

<!-- incompatible:start -->

| Provider | Signs with |
|---|---|
| [Amazon SNS](https://docs.aws.amazon.com/sns/latest/dg/sns-verify-signature-of-message.html) | RSA, with a certificate fetched from an amazonaws.com URL |
| [Apple App Store Server Notifications V2](https://developer.apple.com/documentation/appstoreservernotifications/responsebodyv2) | JWS with an x5c certificate chain, verified to an Apple root |
| [Google Cloud Pub/Sub push](https://docs.cloud.google.com/pubsub/docs/authenticate-push-subscriptions) | OIDC JWT signed by Google, verified against Google's public keys |
| [PayPal](https://developer.paypal.com/api/rest/webhooks/rest/#link-messagesignatureverification) | RSA, with a certificate fetched from a paypal.com URL |

**Amazon SNS.** SNS signs with a certificate your handler fetches over HTTPS from the SigningCertURL in the message. Instead: Publish a real message to a test topic pointed at your endpoint. SNS delivers to any HTTPS URL you have confirmed the subscription on, so a tunnel to localhost is enough.

**Apple App Store Server Notifications V2.** The whole payload is a JWS whose header carries a three-certificate chain that has to verify up to an Apple root. Instead: Use the Request a Test Notification endpoint in the App Store Server API, which makes Apple send a real, signed notification to the URL you have configured.

**Google Cloud Pub/Sub push.** Push subscriptions authenticate with an OIDC bearer token signed by Google, and there is no shared-secret option. Instead: Publish to the topic from the emulator or a test project. Anything reaching your endpoint that is not from Google is supposed to fail, which is the behaviour you are testing.

**PayPal.** PayPal signs with RSA and your handler downloads the certificate from a URL it checks is on paypal.com. Instead: Use PayPal's own webhooks simulator in the developer dashboard, which sends genuinely signed deliveries to a public URL, or call their verify-webhook-signature endpoint with a real captured event.

<!-- incompatible:end -->

That list is `providers/incompatible.yaml`, and it is what the command above
prints, so the two cannot drift. If one of them ever ships a shared secret
scheme, moving it is a pack and a deletion.

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

The placeholders are `{body}`, `{timestamp}`, `{id}`, `{event}`, `{url}`,
`{path}`, `{host}` and `{method}`, plus `{signature}` in `format`. An event is a
file under `events/` named for the event, and its extension sets the content
type. Adding an event is adding one file.

`{url}` exists because Twilio and Square sign the target URL, which is why
signing takes the whole request rather than the body. Nine of the ten providers
need no Go at all. Twilio is the exception, because sorting and deduplicating
form parameters is not a template.

Adding a provider is a pull request against `providers/`. CI checks everything
a pack claims, including against a second implementation of the signing rules
written in Python, so a pack the Go engine reads differently fails there rather
than in somebody's afternoon. `AGENTS.md` has the working rules if you are
driving an agent at it.

## Where packs come from

`herald sync` pulls the current set. Three layers, later wins:

| Layer | Where | What it is |
|---|---|---|
| embedded | in the binary | the snapshot at release, so a fresh install works offline |
| synced | `$XDG_DATA_HOME/herald/providers` | what `herald sync` downloaded. Safe to delete |
| local | `$XDG_CONFIG_HOME/herald/providers` | yours. Sync never touches it |

The split matters if you keep work there. A pack you wrote, or a provider that
is not going public, lives in the config directory and survives every sync.
Point `HERALD_PACKS_REPO` at a fork to ship packs to your own team.

A provider resolves whole rather than merging, so a synced pack never supplies
half of one version's signing rules and half of another's.

## Secrets

In order: `--secret`, then `HERALD_SECRET_STRIPE`, then `HERALD_SECRET`, then
the example in the pack. Set your application to the pack's example and local
testing needs no arguments at all.

herald does not save secrets. The browser remembers your target URL and nothing
else, because a config file full of signing secrets is a worse thing to own
than a paste.

## What it is not

herald does not receive webhooks. For inspecting what a provider really sends,
use webhook.site or [webhook-tester](https://github.com/tarampampam/webhook-tester);
this is the other direction.

It does not replace a provider's own CLI for that provider. `stripe trigger`
creates real test-mode objects, so its payload is better than any fixture can
be. herald is for the case where you are testing ten providers rather than one,
or you are offline, or the provider has no CLI, or you want one interface for
all of them.

And the payloads that ship today were written from each provider's
documentation rather than captured from a live delivery. They are the right
shape with the right fields; they are not byte-identical to production traffic.
Captured events are better, and replacing one is a pull request.

Signatures are a different matter. Those are checked four ways.

## How the signatures are checked

This is the only claim herald makes, so it is worth saying how it is held up.

1. **The providers' own published vectors.** GitHub's and Standard Webhooks'
   documented examples, whose expected values did not originate here.
2. **A second implementation.** `scripts/gen_vectors.py` reads the same packs
   and computes the same signatures in Python. CI regenerates it and fails on a
   diff, so the Go engine and the written-down rule cannot drift apart quietly.
3. **End to end.** Go tests deliver to receivers that verify the way each
   provider's documentation says to.
4. **Over the wire.** `scripts/smoke.sh` runs the built binary against an echo
   service and recomputes signatures from what the far end says it received,
   using `openssl`.

What none of that proves is that a real framework's verification middleware
accepts the delivery. An echo service verifies nothing. If you point herald at
Laravel Cashier or Spatie's webhook client and it misbehaves, that is a bug
worth opening.

## Developing

```bash
go build -o bin/herald ./cmd/herald
go test ./...
scripts/smoke.sh                            # real network, not in CI
scripts/smoke.sh https://httpbin.org/post
```

The smoke test is deliberately out of CI. A test that fails when somebody
else's free service is having a morning teaches you nothing about this code. It
signs with each pack's example secret and drops `HERALD_SECRET` and any
`HERALD_SECRET_*` from its environment first, so a real secret cannot reach a
public echo service by accident.

`httpbingo.org` is the default because it returns the raw body whatever the
content type. `httpbin.org` parses a form body into `.form` and empties
`.data`, so the form providers report that their bytes could not be checked
rather than pretending either way.

Releases are cut by pushing a tag:

```bash
git tag v0.1.1 && git push origin v0.1.1
```

## License

MIT. See [LICENSE](LICENSE).
