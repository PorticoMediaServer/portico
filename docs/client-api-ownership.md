# Client API ownership

Direct server calls belong in `packages/client-core`. The CI step **Client-core API ownership ratchet** counts calls outside that package with the existing TypeScript and native site finders in `ci/client_api_sites.mjs` and `ci/client_api_census.py`.

Run `python3 ci/client_api_census.py --base origin/main` to compare the working tree with its merge base. The check fails when the total increases. Moving a call, renaming its function, or changing its surrounding code needs no approval data. Calls of every HTTP verb and URL builders count, including platform transports previously covered by exceptions. Detection excludes tests and tooling unless production imports them. There are no fingerprints, exception ledgers, approval files or checked-in census baseline.

CI supplies the planner's base and, for a merge group, its immediate predecessor. `--queue-head <sha>` checks every first-parent commit against its own parent as well as the final count against the supplied bases. A later removal cannot conceal an earlier increase. Running without a base prints an inventory only.

The site finder still needs Node and the repository's installed JavaScript dependencies to resolve types. It uses one TypeScript worker; the overlapping engineering `query-read` counter has been removed. The other engineering rules retain their own per-file baselines and comparisons.
