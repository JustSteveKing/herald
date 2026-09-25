// Code generated from the provider packs by a second, independent
// implementation of the signing rules. Regenerate with
// scripts/gen_vectors.py after changing a pack's signing block.

package signer

var packVectors = []packVector{
	{provider: "github", secret: "It's a Secret to Everybody", header: "X-Hub-Signature-256", want: "sha256=f96e2f2d25f3a0a0ebc0d8ab645f4dd49bd0f0f80db32c4b68d047c7b06dd8d0"},
	{provider: "github", secret: "It's a Secret to Everybody", header: "X-Hub-Signature", want: "sha1=b153845637d802d79e5b901afb3afa4ed7729f93"},
	{provider: "mollie", secret: "whsec_mollie_test_secret", header: "X-Mollie-Signature", want: "sha256=10fa9b62485a4ab3bbc068897def989b8a06cf29324e7fdc99729b8affa375c1"},
	{provider: "paddle", secret: "pdl_ntfset_01gt2183qnx16v3g3d3s1s2a1b_secret", header: "Paddle-Signature", want: "ts=1735689600;h1=dae9e91dfc46f3c4a6138a7e6049ece8f64e194a2a172c3adc03355012f19281"},
	{provider: "shopify", secret: "hush", header: "X-Shopify-Hmac-Sha256", want: "0qgAiZj+St/dOktwfRHyqrd5hPy1tVi7miu0TOYKc3s="},
	{provider: "slack", secret: "8f742231b10e8888abcd99yyyzzz85a5", header: "X-Slack-Signature", want: "v0=93af8a900b97e62a9666068b1ef3ca371c9a9bf828ea2190c2d2c957e183072a"},
	{provider: "square", secret: "sq_sig_test_key_1234567890", header: "x-square-hmacsha256-signature", want: "XjvK18DJGlJ1yUIkuGUoxK9nDBrdumchJ4geWJfbvkE="},
	{provider: "standardwebhooks", secret: "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", header: "webhook-signature", want: "v1,3JxL7PQ81JFs80RBfprcBVIkfqhRaCqSMWl2PyEaTs4="},
	{provider: "stripe", secret: "whsec_test_secret", header: "Stripe-Signature", want: "t=1735689600,v1=971bbf0475ded6280668081bedabf2af934ca05f2794e0d13a17058724f8c496"},
}
