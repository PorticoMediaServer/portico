# Scale invocations

From the pinned server checkout on `portico-runner` or mini-2:

```sh
scripts/batch-round/scalebench.sh --items 1000000
scripts/batch-round/scalebench.sh --items 5000000
# After batch start, append --execute. Mini-2 light rehearsal:
scripts/batch-round/scalebench.sh --tiny --execute
```

Default is plan-only. Full runs use 20 samples, a 1-minute catalogue-only idle observation and `-exercise` (includes events and write/rebuild scenarios). Each retains an independent lane with stdout/stderr, receipt and the database. Read stdout route errors as well as exit status: this is a measurement tool, not a complete acceptance evaluator. No thresholds or scratch guards are changed.

Existing scalebench stops seeding when its independent lane reaches 7 GB (8 GB hard budget). A full 5M fixture might hit that guard: report a blocked capacity measurement and design a proportional fixture/storage change; never bypass it or call a smaller run 5M. Its fake asset paths, keys and viewer fixtures do not constitute a runnable full-server installation. Full-server 5M provisioning, large-playlist display-order paging and cache byte accounting require the separate proposals in BATCH-ROUND-GAPS.md.

Do not run both scales at once. Need >25 GiB free before a run; preserve at least 20 GiB afterwards (15 GiB floor on mini-2). Stop and remove only this lane's disposable fixture after recording source hash and results and after the backup comparison no longer needs it. Estimated planning windows: 1M 1–3 hours; 5M 3–8 hours, unmeasured estimates, stop on the existing budget rather than extending it. Tiny is not acceptance.
