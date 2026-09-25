"""Render providers/incompatible.yaml into the README.

The list is data so that the CLI can explain itself when someone names one of
these. The README has to say the same thing, and a hand-copied second version
of a list is a list that goes wrong. CI regenerates this and fails on a diff.
"""
import sys, yaml

START = "<!-- incompatible:start -->"
END = "<!-- incompatible:end -->"


def block(entries):
    """Table plus one short paragraph each.

    The long-form `detail` deliberately does not come out here. It is the
    explanation a contributor wants when they are arguing with the decision,
    and it lives in providers/incompatible.yaml where the decision is. A README
    section that runs four paragraphs per entry is a section people scroll
    past, which defeats the point of listing them at all.
    """
    out = [START, ""]
    out.append("| Provider | Signs with |")
    out.append("|---|---|")
    for e in entries:
        out.append("| [%s](%s) | %s |" % (e["name"], e["docs"], e["scheme"]))
    out.append("")

    for e in entries:
        out.append("**%s.** %s Instead: %s" % (
            e["name"],
            " ".join(e["summary"].split()),
            " ".join(e["workaround"].split()),
        ))
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
