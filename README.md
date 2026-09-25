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

Ten packs ship in the binary: `stripe`, `mollie`, `mollie-classic`, `github`,
`shopify`, `slack`, `twilio`, `paddle`, `square` and `standardwebhooks`.

`standardwebhooks` covers everything that adopted the spec, which is most of
what sends through Svix: Clerk, Resend and others. If your endpoint reads
`webhook-id` and `webhook-signature`, that is the pack.

One of them will not do what you expect, and says so before it sends.
`mollie-classic` is unsigned and carries no payload: the body is `id=tr_...`
and Mollie expects your application to call the API and read the status back.
Sending it makes your application call the real Mollie API with an id that does
not exist in your account, so it covers the route and not the logic.

## Providers herald will not ship

If verifying a delivery means fetching a public key or a certificate from a
domain the provider controls, there is no shared secret and nothing outside
that provider can produce a delivery that verifies.

A pack that sent the payload with no valid signature would be worse than no
pack. It passes against a handler that never verifies anything, which is
exactly the handler worth catching, and it reports that as a green result. So
these are absent on purpose, and naming one tells you what to use instead:

```
$ herald send paypal PAYMENT.CAPTURE.COMPLETED --to $URL
herald: PayPal cannot be faked, so herald does not ship a pack for it.
```

That is not the same as a provider which does not sign at all. Mollie's classic
webhook has no signature by design, so herald reproduces it exactly and ships
it. Absent by design is reproducible; absent because you cannot forge it is
not.

<!-- incompatible:start -->

| Provider | Signs with |
|---|---|
| [Amazon SNS](https://docs.aws.amazon.com/sns/latest/dg/sns-verify-signature-of-message.html) | RSA, with a certificate fetched from an amazonaws.com URL |
| [Apple App Store Server Notifications V2](https://developer.apple.com/documentation/appstoreservernotifications/responsebodyv2) | JWS with an x5c certificate chain, verified to an Apple root |
| [Google Cloud Pub/Sub push](https://docs.cloud.google.com/pubsub/docs/authenticate-push-subscriptions) | OIDC JWT signed by Google, verified against Google's public keys |
| [PayPal](https://developer.paypal.com/api/rest/webhooks/rest/#link-messagesignatureverification) | RSA, with a certificate fetched from a paypal.com URL |

**Amazon SNS.** SNS signs with a certificate your handler fetches over HTTPS from the SigningCertURL in the message.

The message carries Signature, SignatureVersion and SigningCertURL. AWS tells you to fetch that certificate over HTTPS, confirm it was issued by Amazon SNS and that its chain of trust is valid, then verify. There is no shared secret at any point.

Instead: Publish a real message to a test topic pointed at your endpoint. SNS delivers to any HTTPS URL you have confirmed the subscription on, so a tunnel to localhost is enough.

**Apple App Store Server Notifications V2.** The whole payload is a JWS whose header carries a three-certificate chain that has to verify up to an Apple root.

There is no separate signature header, because the notification itself is a signed JWT. Apple's own verification library reads the x5c header, requires a chain of exactly three certificates, and verifies the leaf against Apple's root certificates before trusting the payload. Signing one means holding Apple's private key.

Instead: Use the Request a Test Notification endpoint in the App Store Server API, which makes Apple send a real, signed notification to the URL you have configured.

**Google Cloud Pub/Sub push.** Push subscriptions authenticate with an OIDC bearer token signed by Google, and there is no shared-secret option.

The Authorization header carries a JWT signed by the Pub/Sub service. Your handler verifies it against Google's public certificates and checks the email and audience claims. Google documents no shared secret and no token parameter as an alternative, so the only way to produce an acceptable token is to be Google.

Instead: Publish to the topic from the emulator or a test project. Anything reaching your endpoint that is not from Google is supposed to fail, which is the behaviour you are testing.

**PayPal.** PayPal signs with RSA and your handler downloads the certificate from a URL it checks is on paypal.com.

The delivery carries paypal-transmission-sig, paypal-cert-url and paypal-auth-algo. Verification downloads the certificate named by that URL, having first checked the URL is a paypal.com domain, and verifies the signature against the public key in it. Nothing in that chain involves a secret you hold.

Instead: Use PayPal's own webhooks simulator in the developer dashboard, which sends genuinely signed deliveries to a public URL, or call their verify-webhook-signature endpoint with a real captured event.

<!-- incompatible:end -->

The list lives in `providers/incompatible.yaml` and is what the command above
prints, so the two cannot drift. If one of these ever ships a shared secret
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

## Smoke test

`scripts/smoke.sh` sends every fixture over a real network to an echo service
and reads back what that service says it received.

```bash
scripts/smoke.sh                            # https://httpbingo.org/post
scripts/smoke.sh https://httpbin.org/post
```

The Go suite proves the signatures three ways, but all three run in process
against receivers written in this repository. This runs the built binary, over
TLS, through a server nobody here wrote, and checks that the body survives byte
for byte, that every header arrives, and that the content type is what the pack
asked for. Body mangling between signing and sending would be invisible to the
unit tests and fatal in practice, because every scheme here signs raw bytes.

It then recomputes three signatures from the wire with `openssl`, which is a
fourth implementation after the Go engine, the Python generator and the Go
tests. Then it exercises the delivery controls, including checking that
`--duplicate` makes the receiver see one delivery id and that leaving it off
makes the receiver see two.

It signs with each pack's example secret and drops `HERALD_SECRET` and any
`HERALD_SECRET_*` from its environment first, so a real secret cannot reach a
public echo service by accident.

What it cannot prove is that a real framework's verification middleware accepts
the delivery. An echo service verifies nothing. That needs a handler, and it is
the next thing worth doing.

It is not in CI, on purpose. A test that fails when somebody else's free
service is having a morning teaches you nothing about this code.

`httpbingo.org` is the default because it returns the raw body whatever the
content type. `httpbin.org` parses a form body into `.form` and empties
`.data`, so the three form providers report that their bytes could not be
checked rather than pretending either way.

## Honest about the fixtures

The payloads that ship today were written from each provider's documentation,
not captured from a live delivery. They are the right shape and the right
fields; they are not byte-identical to production traffic. Captured events are
better and replacing one is a pull request.

Signatures are a different matter, and are checked against GitHub's and
Standard Webhooks' own published test vectors.
