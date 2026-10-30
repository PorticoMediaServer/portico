#!/usr/bin/env python3
"""Check the G03/G04 reports. Fails on a missing page, a skipped/duplicated
occurrence, a broken filter count, a reorder that lost the applied order, a
prepared-read budget overrun, or a database change caused by query churn.

Facet, across-order and count counters come from the approved exact
Service.QueryMemoryStats observer; RSS and compile-time limits are never
accepted in their place.
"""
import argparse, json, sys


def load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def playlist_problems(report, requested, bounded):
    problems = []
    if report.get("entries", 0) < requested:
        problems.append(f"playlist has {report.get('entries')} entries, want {requested}")
    sorts = report.get("sorts", [])
    seen = {(s.get("sort"), s.get("direction")) for s in sorts}
    for needed in [("position", "asc"), ("title", "asc"), ("title", "desc"),
                   ("year", "asc"), ("year", "desc"), ("duration", "asc"), ("duration", "desc")]:
        if needed not in seen:
            problems.append(f"missing display order {needed}")
    for sort in sorts:
        if sort.get("total") != requested:
            problems.append(f"{sort.get('route')} total {sort.get('total')} != {requested}")
        if not bounded and not sort.get("reachedLast"):
            problems.append(f"{sort.get('route')} did not reach the last page")
        if not sort.get("firstPageMs") or not sort.get("deepPageMs"):
            problems.append(f"{sort.get('route')} did not time first/deep")
        if not bounded and not sort.get("lastPageMs"):
            problems.append(f"{sort.get('route')} did not time the last page")
    for ties in report.get("ties", []):
        if ties.get("duplicateTokens") or ties.get("missingTokens"):
            problems.append(f"tie walk {ties.get('sort')} skipped or repeated occurrences")
        if not bounded and not ties.get("everyOccurrenceExactlyOnce"):
            problems.append(f"tie walk {ties.get('sort')} was not a complete total order")
    for filters in report.get("filters", []):
        if not filters.get("watchedAndUnwatchedWithinAll"):
            problems.append(f"viewer {filters.get('viewer')} watched/unwatched exceed all")
        if not filters.get("inProgressWithinUnwatched"):
            problems.append(f"viewer {filters.get('viewer')} in-progress exceeds unwatched")
    reorder = report.get("concurrentReorder", {})
    if reorder.get("writes", 0) and not reorder.get("expectedOrderPreserved"):
        problems.append("concurrent reorder did not preserve the applied order")
    if reorder.get("writes", 0) and not reorder.get("reads"):
        problems.append("no reader ran during concurrent reorder")
    return problems


def memory_problems(report, require_expiry=False):
    problems = []
    if require_expiry and not report.get("expiryRestart", {}).get("facetRecomputedAfterTtl"):
        problems.append("facet TTL recomputation was not observed")
    prepared = report.get("preparedReadBytes", {})
    if prepared.get("maxChargedBytes", 0) > prepared.get("budgetBytes", 0):
        problems.append("prepared reads exceeded the store budget")
    if not prepared.get("exact"):
        problems.append("prepared read accounting was not exact")
    if not report.get("expiryRestart", {}).get("preparedHandlesLostOnRestart"):
        problems.append("prepared handles survived a restart")
    inventory = report.get("inventory", {})
    if not inventory.get("schemaEqual"):
        problems.append("query churn changed the schema")
    if inventory.get("countDeltas"):
        problems.append(f"query churn changed stored rows: {inventory.get('countDeltas')}")
    if not inventory.get("noStoredQueryState"):
        problems.append("query churn left stored query state")
    if not report.get("cacheCountersExact"):
        problems.append("exact facet/across/count counters were not reported")
    budgets = report.get("cacheBudgets", {})
    peak = report.get("cacheStatsPeak", {})
    for field, budget_field in (("FacetBytes", "facetBudgetBytes"), ("AcrossBytes", "acrossBudgetBytes")):
        budget = budgets.get(budget_field, 0)
        if budget and peak.get(field, 0) > budget:
            problems.append(f"peak {field}={peak.get(field)} over budget {budget}")
    if budgets.get("facetCapacity") and peak.get("FacetEntries", 0) > budgets["facetCapacity"]:
        problems.append("facet entries over capacity")
    if budgets.get("acrossCapacity") and peak.get("AcrossEntries", 0) > budgets["acrossCapacity"]:
        problems.append("across-order entries over capacity")
    for probe in report.get("facetPages", []):
        if probe.get("retainedBytes", 0) > 16 * 1024 * 1024:
            problems.append(f"{probe.get('library')}/{probe.get('field')} single list over 16 MiB")
    return problems


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--playlist", required=True)
    parser.add_argument("--memory", required=True)
    parser.add_argument("--requested", type=int, required=True)
    parser.add_argument("--bounded", type=int, default=0)
    args = parser.parse_args()
    problems = playlist_problems(load(args.playlist), args.requested, args.bounded)
    problems += memory_problems(load(args.memory), args.requested >= 100000)
    if problems:
        for problem in problems:
            print(f"FAIL: {problem}", file=sys.stderr)
        return 1
    print(json.dumps({"result": "pass", "entries": args.requested}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
