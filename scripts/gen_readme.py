"""Render providers/incompatible.yaml into the README.

The list is data so that the CLI can explain itself when someone names one of
these. The README has to say the same thing, and a hand-copied second version
of a list is a list that goes wrong. CI regenerates this and fails on a diff.
"""
import sys, yaml

START = "<!-- incompatible:start -->"
END = "<!-- incompatible:end -->"


def block(entries):
    out = [START, ""]
    out.append("| Provider | Signs with |")
    out.append("|---|---|")
    for e in entries:
        out.append("| [%s](%s) | %s |" % (e["name"], e["docs"], e["scheme"]))
    out.append("")

    for e in entries:
        out.append("**%s.** %s" % (e["name"], e["summary"]))
        out.append("")
        out.append(" ".join(e["detail"].split()))
        out.append("")
        out.append("Instead: %s" % " ".join(e["workaround"].split()))
        out.append("")

    out.append(END)
    return "\n".join(out)


def main():
    entries = yaml.safe_load(open("providers/incompatible.yaml"))["incompatible"]
    entries.sort(key=lambda e: e["name"])

    readme = open("README.md").read()
    before, rest = readme.split(START, 1)
    _, after = rest.split(END, 1)

    open("README.md", "w").write(before + block(entries) + after)


if __name__ == "__main__":
    sys.exit(main())
