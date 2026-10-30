# Contributing

Thanks for wanting to help. Before you put time into something, here's how this works.

Portico tries to do fewer things and do them really well. It isn't meant to be a pile of options and plugins. I'm the only maintainer, and I make the calls on design, scope, architecture, releases and branding. That means a change can be well built and still not be right for Portico, and I may say no to it.

## What's welcome

- Bug fixes
- Reliability and performance improvements
- Better support for devices, formats and platforms
- Documentation fixes

Small, obvious fixes can go straight to a pull request. For anything else, including new features and changes to how something behaves, open an issue or start a discussion first, and wait until I've said yes. A yes covers only what we agreed on.

## What I'm not looking for

- New features nobody asked for
- More settings, plugins, themes or custom CSS
- New dependencies I haven't agreed to
- Translations
- Cleanups or refactors on their own
- Changes to the branding or to how Portico Services works

UI changes need to match the approved design. Keep each pull request to one problem.

## What to include in a pull request

- The issue it fixes, or a line on why the fix is obvious
- How to reproduce the problem, and what you saw before and after
- Tests for the change, and anything you couldn't test
- Before and after screenshots for UI changes. Use a recording for anything to do with motion or timing, and show where focus goes for TV changes.
- Who wrote it: you, or which AI model and tool. AI-written code is fine and welcome, but you're still responsible for it.
- Any third-party code you copied in, and its licence
- Your agreement to the [CLA](CLA.md). If your employer owns your work, they'll need to sign a company agreement too.

Passing tests don't prove a bug is fixed, so show me that it is.

## What happens next

I'll get to pull requests when I can, but there's no promised timeline and no guarantee of a review or a merge. I might decline something, park it, or fix the same problem a different way. Pull requests that skip the steps above may be closed. AI agents may help me triage, but I decide what gets merged, and no agent merges outside contributions.

Pull requests from forks run CI on GitHub's own runners, without access to any secrets.

## Where things go

- Bugs: [Issues](https://github.com/PorticoMediaServer/portico/issues)
- Ideas and questions: [Discussions](https://github.com/PorticoMediaServer/portico/discussions)
- Security problems: privately, as described in [SECURITY.md](SECURITY.md)

## Why there's a CLA

Portico's apps go into app stores, and Portico Services runs as a hosted service. Both can need terms other than the GPL. The CLA lets me use contributions there while you keep your copyright. Anything already released under the GPL stays available under the GPL, and no app feature will ever be put behind a paywall.
