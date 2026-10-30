#!/usr/bin/env python3
"""The Go build tags every official Portico server build must carry.

One list, not a copy in each builder: `scripts/build-server-release.py`, the
Dockerfile, CI, the QA runner, the Makefile and the packaging seals all read it
here. A command that builds `./cmd/server` without these tags silently ships a
server whose comic archive readers are absent, so `--check` lets a CI guard fail
a changed command instead of leaving the capability to a reviewer's memory.

Two sets, because they answer different questions:

* CAPABILITY is the compile-time reader capability that every official build
  needs. CI compiles and tests it (so tagged-only code cannot rot) and release
  artifacts ship it.
* RELEASE adds the certified-release marker, which only the sealed release
  builder and the released container image may set: it changes the trust root
  and must never be a local or test-helper tag.

Both shipped basic comic formats are in the capability set:

* `portico_rar7z` compiles the pinned, bounded RAR5 reader (CBR).
* `portico_cb7` selects the existing CB7 surface. It does not by itself open a
  CB7: `readingcap.CB7()` additionally requires a supported platform, the reader
  goes through the owned resource-bounded `archive7z` worker, and
  `reading/archive_classic.go` still refuses an in-process CB7 regardless of
  tags. The tag is the same literal one the private QA/Contabo builders already
  carried; it now rides with `portico_rar7z` in the official list instead of
  being a separate gate.

Usage:
  scripts/release-build-tags.py              # release tag argument
  scripts/release-build-tags.py --capability # capability-only tag argument
  scripts/release-build-tags.py --check TAGS # exit 1 when TAGS is incomplete
"""
import re
import sys

CAPABILITY = ("portico_rar7z", "portico_cb7")
RELEASE = ("release",) + CAPABILITY


def value(release=True):
    return ",".join(RELEASE if release else CAPABILITY)


def required(release=True):
    return frozenset(RELEASE if release else CAPABILITY)


def tags(tag_argument):
    """The tags in a `go build -tags` argument, accepting commas or spaces."""
    return frozenset(part for part in re.split(r"[,\s]+", (tag_argument or "").strip()) if part)


def covers(tag_argument, release=True):
    """Whether a compiler `-tags` value is the official tag set."""
    return required(release) <= tags(tag_argument)


def main(argv):
    release = "--capability" not in argv
    if "--check" in argv:
        index = argv.index("--check")
        supplied = argv[index + 1] if index + 1 < len(argv) else ""
        missing = sorted(required(release) - tags(supplied))
        if missing:
            print("missing official build tags: " + ", ".join(missing), file=sys.stderr)
            return 1
        print("official build tags present: " + value(release))
        return 0
    print(value(release))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
