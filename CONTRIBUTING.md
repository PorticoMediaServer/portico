# Contributing

Portico does fewer things and does them really well. It is not a collection of options and plugins. Justin Ehler is the sole maintainer and decides design, architecture, scope, releases and brand. A technically sound change can still be wrong for Portico.

## Before starting

Features and behavior changes need a maintainer-approved issue. Approval covers only the agreed scope. Obvious fixes may be opened directly. A contribution creates no entitlement to a response, review or merge; it may be declined, deferred, closed or reimplemented.

We welcome focused bug fixes, reliability and performance improvements, device/format/platform compatibility, and approved documentation work.

We do not accept unsolicited features, additional settings, plugins, themes, custom CSS, unapproved dependencies, translations, drive-by cleanup or refactors, or changes to branding or the Portico Services integration. UI changes must match the approved design. Keep each pull request about one underlying problem.

## Pull request requirements

- A linked approved issue, or an explanation of the obvious fix.
- Reproduction steps, observed results, focused tests and checks you could not complete.
- Before/after screenshots for UI changes, recordings for motion or timing, and focus evidence for TV interactions.
- Disclosure of who wrote the change: a person, or the model(s) and harness used. AI-written code is welcome; you remain responsible for it.
- Disclosure of third-party code and its licence.
- Agreement to the current [CLA](CLA.md) before review. Employer-owned work needs a corporate agreement from an authorized representative.

Passing tests alone does not prove a bug is fixed. Follow the build instructions and keep unrelated changes out.

## Review and acceptance

The contribution gate checks the CLA, approval, completed template and CI. An agent may prepare a triage/review report; Justin decides acceptance. Agents do not merge outside contributions. Policy failures may be closed automatically, including drafts. There is no promised review time.

Fork pull requests run on GitHub-hosted runners without secrets. They must never execute contributed code on a self-hosted runner or through a privileged pull_request_target checkout.

Bugs belong in Issues; ideas belong in Discussions → Ideas. Security reports follow [SECURITY.md](SECURITY.md).

## Why the CLA exists

App stores and Portico Services may require different terms from the GPL. The CLA permits those uses while contributors keep copyright. Code already released under the GPL remains available under the GPL, and app capabilities will not be paywalled.
