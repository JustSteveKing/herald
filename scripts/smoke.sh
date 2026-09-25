#!/usr/bin/env bash
#
# Send every fixture over a real network and check what actually arrived.
#
# The Go suite already proves the signatures three ways, but all three run in
# process against receivers written here. This runs herald as a binary, over
# TLS, through an HTTP server nobody here wrote, and reads back what that
# server says it received.
#
# What it can prove, and the reason it is worth running: the body survives the
# wire byte for byte, and the headers arrive with the values and the casing
# herald set. Body mangling between signing and sending would be invisible to
# the unit tests and fatal in practice, because every scheme here signs the
# raw bytes.
#
# What it cannot prove: that a real framework's verification middleware accepts
# the delivery. An echo service does not verify anything. That needs a handler.
#
# Usage:
#   scripts/smoke.sh                       # https://httpbingo.org/post
#   scripts/smoke.sh https://httpbin.org/post
#   scripts/smoke.sh http://localhost:8080/post
#
# Any target that answers with the httpbin JSON shape will do: a .data string
# holding the raw body, and a .headers object. httpbin returns header values as
# strings and httpbingo as arrays; both are handled.

set -uo pipefail

TARGET="${1:-${HERALD_SMOKE_TARGET:-https://httpbingo.org/post}}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HERALD="$ROOT/bin/herald"

# The target is usually a public service, so no real secret is allowed near it.
# Everything below signs with the example secret out of the pack, and any
# secret in the environment is dropped for the duration rather than trusted not
# to be used.
unset HERALD_SECRET
while IFS= read -r name; do unset "$name"; done < <(env | sed -n 's/^\(HERALD_SECRET_[A-Z0-9_]*\)=.*/\1/p')
unset HERALD_TARGET

pass=0
fail=0
skip=0

red() { printf '\033[31m%s\033[0m' "$1"; }
green() { printf '\033[32m%s\033[0m' "$1"; }
dim() { printf '\033[2m%s\033[0m' "$1"; }

ok() { pass=$((pass + 1)); printf '  %s %s\n' "$(green ok)" "$1"; }
no() {
  fail=$((fail + 1))
  printf '  %s %s\n' "$(red FAIL)" "$1"
  [ $# -gt 1 ] && printf '       %s\n' "$2"
}
note() { skip=$((skip + 1)); printf '  %s %s\n' "$(dim '--')" "$(dim "$1")"; }

# header_present <echo json> <name> answers whether the header arrived at all,
# which is not the same question as whether it has a value. An empty header
# travels the wire intact and echoes back as "", so a check for a non-empty
# value reports it missing and sends you looking for a bug in the sending.
header_present() {
  jq -e --arg name "$2" '
    (.headers // {}) | keys | map(ascii_downcase) | index($name | ascii_downcase) != null
  ' >/dev/null 2>&1 <<<"$1"
}

# header_value <echo json> <name> reads a header out of either echo shape.
header_value() {
  jq -r --arg name "$2" '
    (.headers // {})
    | to_entries
    | map(select(.key | ascii_downcase == ($name | ascii_downcase)))
    | first
    | if . == null then ""
      elif (.value | type) == "array" then (.value | first)
      else .value end
  ' <<<"$1"
}

# example_secret <provider> pulls the documented example out of the pack, so
# the script never carries its own copy to go stale.
example_secret() {
  sed -n 's/^  example: //p' "$ROOT/providers/$1/provider.yaml" | head -1 | tr -d '"'
}

hmac_hex() { printf '%s' "$2" | openssl dgst -"$3" -hmac "$1" | awk '{print $NF}'; }
hmac_b64() { printf '%s' "$2" | openssl dgst -"$3" -hmac "$1" -binary | base64; }

if [ ! -x "$HERALD" ]; then
  echo "Building herald"
  (cd "$ROOT" && go build -o bin/herald ./cmd/herald) || exit 1
fi

printf '%s\n' "herald $("$HERALD" --version | awk '{print $NF}') against $TARGET"
printf '%s\n\n' "$(dim 'Signing with each pack example secret. Any HERALD_SECRET in the environment was dropped.')"

# ---------------------------------------------------------------------------
# Every fixture, checked against what the server says it received.
# ---------------------------------------------------------------------------

providers=$("$HERALD" providers | awk 'NR>1 {if (!NF) exit; print $1}')

# json_objects strips herald's own output so that jq sees only the response
# bodies. A run with --count prints several, which jq reads in sequence quite
# happily once the plain text is out of the way.
#
# Both patterns are anchored at column zero, and every line of an indented JSON
# body except its braces starts with a space, so nothing inside a response can
# be mistaken for herald's chrome. An earlier version of this stripped
# four-space lines to catch error detail, and quietly ate every nested field of
# the echo it was supposed to be reading.
json_objects() { grep -vE '^(ok|bad|err) |^! ' ; }

for provider in $providers; do
  printf '%s\n' "$provider"

  for event in $("$HERALD" events "$provider" | awk '!/^ / && NF {print $1}'); do
    body=$("$HERALD" show "$provider" "$event")

    # The header names herald intends to send. Only the names: a dry run is a
    # different delivery from the one that follows, with its own id and its own
    # timestamp, so comparing those values would be comparing two deliveries
    # and calling the difference a bug. Values that should be identical every
    # time are checked below, and the ones that should not are recomputed from
    # the wire in the next section.
    intended_names=$("$HERALD" send "$provider" "$event" --to "$TARGET" --dry-run 2>/dev/null |
      sed -n '/^POST /,/^$/p' | sed '1d;$d' | sed 's/:.*//')

    response=$("$HERALD" send "$provider" "$event" --to "$TARGET" --response 2>&1)
    status=$(printf '%s\n' "$response" | awk '/^(ok|bad|err) /{print $4; exit}')

    if [ "$status" != "200" ]; then
      no "$event" "status $status"
      continue
    fi

    echoed=$(printf '%s\n' "$response" | json_objects)
    if ! jq -e . >/dev/null 2>&1 <<<"$echoed"; then
      no "$event" "the response was not the JSON echo this script expects"
      continue
    fi

    # 1. The body has to come back exactly as it went out, because that is what
    #    every signature here was computed over. This is the check worth having.
    #
    #    Except when the service will not give it back. httpbin.org parses a
    #    form body into .form and leaves .data empty, so the bytes are gone and
    #    there is nothing to compare. That is the echo service destroying the
    #    evidence rather than a delivery going wrong, so it says so and carries
    #    on with the checks it can still make. httpbingo.org is the default
    #    because it keeps the raw body whatever the content type.
    got_body=$(jq -r '.data // ""' <<<"$echoed")
    body_note=""

    if [ -z "$got_body" ] && [ "$(jq -r '(.form // {}) | length' <<<"$echoed")" != "0" ]; then
      body_note=" $(dim '(form body parsed away by this service, bytes unchecked)')"
    elif [ "$got_body" != "$body" ]; then
      no "$event" "the body changed in transit"
      continue
    fi

    # 2. Every header herald set has to arrive. A dropped one is silent
    #    otherwise, and a missing signature header reads as a verification bug
    #    at the other end.
    missing=""
    while IFS= read -r name; do
      [ -z "$name" ] && continue
      header_present "$echoed" "$name" || { missing="$name"; break; }
    done <<<"$intended_names"

    if [ -n "$missing" ]; then
      no "$event" "$missing never arrived"
      continue
    fi

    # 3. Content-Type is the one value that must be identical every time, and
    #    getting it wrong is how a form body is parsed as JSON.
    want_ct=$("$HERALD" send "$provider" "$event" --to "$TARGET" --dry-run 2>/dev/null |
      sed -n 's/^Content-Type: //p' | head -1)
    got_ct=$(header_value "$echoed" "Content-Type")
    if [ "$got_ct" != "$want_ct" ]; then
      no "$event" "content type arrived as '$got_ct', not '$want_ct'"
      continue
    fi

    ok "$event$body_note"
  done
done

# ---------------------------------------------------------------------------
# A fourth implementation, in openssl, over the body the server received.
#
# The Go engine, the Python generator and the Go tests all read the same packs.
# These three recompute from the wire, with different tools, for the three
# shapes that cover most providers: hex over a timestamped payload, hex over
# the body, and base64 over the body.
# ---------------------------------------------------------------------------

printf '\n%s\n' "recomputed from the wire with openssl"

verify_echo() {
  local provider="$1" event="$2" header="$3" kind="$4"
  local secret response echoed body value

  secret=$(example_secret "$provider")
  response=$("$HERALD" send "$provider" "$event" --to "$TARGET" --response 2>&1)
  echoed=$(printf '%s' "$response" | sed -n '/^[[:space:]]*{/,$p')

  if ! jq -e . >/dev/null 2>&1 <<<"$echoed"; then
    note "$provider: no echo to check"
    return
  fi

  body=$(jq -r '.data // ""' <<<"$echoed")
  value=$(header_value "$echoed" "$header")

  case "$kind" in
    stripe)
      local ts sig want
      ts=$(sed -n 's/^t=\([0-9]*\),.*/\1/p' <<<"$value")
      sig=$(sed -n 's/.*,v1=\(.*\)$/\1/p' <<<"$value")
      want=$(hmac_hex "$secret" "$ts.$body" sha256)
      [ "$sig" = "$want" ] && ok "$provider $header" || no "$provider $header" "recomputed $want, received $sig"
      ;;
    prefixed_hex)
      local sig want
      sig="${value#*=}"
      want=$(hmac_hex "$secret" "$body" sha256)
      [ "$sig" = "$want" ] && ok "$provider $header" || no "$provider $header" "recomputed $want, received $sig"
      ;;
    plain_b64)
      local want
      want=$(hmac_b64 "$secret" "$body" sha256)
      [ "$value" = "$want" ] && ok "$provider $header" || no "$provider $header" "recomputed $want, received $value"
      ;;
  esac
}

verify_echo stripe payment_intent.succeeded Stripe-Signature stripe
verify_echo github push X-Hub-Signature-256 prefixed_hex
verify_echo shopify orders.create X-Shopify-Hmac-Sha256 plain_b64

# ---------------------------------------------------------------------------
# The delivery controls, which are the reason this is not a curl with a file.
# ---------------------------------------------------------------------------

printf '\n%s\n' "delivery controls"

sent_ok() {
  local label="$1"
  shift
  local out status
  out=$("$HERALD" send "$@" --to "$TARGET" 2>&1)
  status=$(printf '%s' "$out" | awk '/^(ok|bad|err) /{print $4; exit}')
  if [ "$status" = "200" ]; then ok "$label"; else no "$label" "status $status"; fi
}

# A retry is the same delivery twice, so the receiver has to see one id. The
# same send without --duplicate has to show two. Checking only the first would
# pass against a build that had stopped generating ids at all.
delivery_ids() {
  "$HERALD" send standardwebhooks user.created --to "$TARGET" --count 2 --response "$@" 2>&1 |
    json_objects |
    jq -r '(.headers["Webhook-Id"] // .headers["webhook-id"] // []) | if type == "array" then first else . end'
}

distinct_dup=$(delivery_ids --duplicate | sort -u | grep -c .)
distinct_fresh=$(delivery_ids | sort -u | grep -c .)

if [ "$distinct_dup" = "1" ]; then
  ok "duplicate sends one delivery id twice"
else
  no "duplicate sends one delivery id twice" "the receiver saw $distinct_dup distinct ids"
fi

if [ "$distinct_fresh" = "2" ]; then
  ok "without duplicate each send is its own delivery"
else
  no "without duplicate each send is its own delivery" "the receiver saw $distinct_fresh distinct ids"
fi

sent_ok "burst of 10" github push --count 10
sent_ok "aged six minutes" stripe payment_intent.succeeded --skew -6m
sent_ok "broken signature still well formed" stripe payment_intent.succeeded --bad-signature
sent_ok "extra header" slack url_verification --header "X-Smoke=1"
sent_ok "unsigned provider" mollie-classic payment.status-changed
sent_ok "url signed provider" twilio sms.received

printf '\n%s\n' "$pass passed, $fail failed, $skip skipped"
[ "$fail" -eq 0 ]
