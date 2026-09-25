"""Second, independent implementation of the pack signing rules.

Reads each provider.yaml and computes what herald should produce for a fixed
delivery. The Go test asserts these exact strings, so the two implementations
have to agree or the build fails.
"""
import base64, glob, hashlib, hmac, os, yaml

BODY = '{"hello":"world"}'
TS = 1735689600
URL = "http://localhost:8000/webhooks"
ID = "msg_2mKqR7vXbN3pLwZs8Yt1Fd"
EVENT = "test.event"

ALGS = {"sha1": hashlib.sha1, "sha256": hashlib.sha256, "sha512": hashlib.sha512}

def fmt_ts(cfg):
    f = (cfg or {}).get("format", "unix")
    if f == "unixmilli":
        return str(TS * 1000)
    if f == "rfc3339":
        import datetime
        return datetime.datetime.fromtimestamp(TS, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    return str(TS)

def expand(s, ts):
    from urllib.parse import urlparse
    u = urlparse(URL)
    for k, v in [("{body}", BODY), ("{timestamp}", ts), ("{id}", ID), ("{event}", EVENT),
                 ("{url}", URL), ("{path}", u.path), ("{host}", u.netloc), ("{method}", "POST")]:
        s = s.replace(k, v)
    return s

def key_for(secret_cfg, secret):
    prefix = (secret_cfg or {}).get("prefix", "")
    if prefix and secret.startswith(prefix):
        secret = secret[len(prefix):]
    if (secret_cfg or {}).get("encoding") == "base64":
        return base64.b64decode(secret)
    return str(secret).encode()

rows = []
for path in sorted(glob.glob("providers/*/provider.yaml")):
    pid = os.path.basename(os.path.dirname(path))
    p = yaml.safe_load(open(path))
    sigs = p.get("signing") or []
    if not sigs:
        continue
    secret = (p.get("secret") or {}).get("example") or ""
    if not secret:
        continue
    key = key_for(p.get("secret"), secret)
    ts = fmt_ts(p.get("timestamp"))
    for sig in sigs:
        if (sig.get("scheme") or "hmac") == "builtin":
            continue
        digest = hmac.new(key, expand(sig["payload"], ts).encode(), ALGS[sig["algorithm"]]).digest()
        rendered = base64.b64encode(digest).decode() if sig["encoding"] == "base64" else digest.hex()
        value = expand(sig.get("format") or "{signature}", ts).replace("{signature}", rendered)
        rows.append((pid, secret, sig["header"], value))

print("// Code generated from the provider packs by a second, independent")
print("// implementation of the signing rules. Regenerate with")
print("// scripts/gen_vectors.py after changing a pack's signing block.")
print()
print("package signer")
print()
print("var packVectors = []packVector{")
for pid, secret, header, value in rows:
    q = lambda v: '"' + str(v).replace('\\', '\\\\').replace('"', '\\"') + '"'
    print("\t{provider: %s, secret: %s, header: %s, want: %s}," % (q(pid), q(secret), q(header), q(value)))
print("}")
