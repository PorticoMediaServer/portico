import type {ReadItemEligibility} from './navigation';

/** The already-authorized eligibility row, never a fresh probe. */
export type ReadVersion = ReadItemEligibility['versions'][number];

/** A real router entry. It is identified exactly as the reader already identifies the
 * current root: `state.location.state.__TSR_key`, never `history.location.key`. */
export type ReadRouteEntry = Readonly<{href: string; key: string}>;

export function readRouteEntry(location: Readonly<{href: string; state: {__TSR_key?: string}}>): ReadRouteEntry | undefined {
  const key = location.state.__TSR_key;
  return typeof key === 'string' && key.length > 0 ? {href: location.href, key} : undefined;
}

export function readSameRouteEntry(left: ReadRouteEntry | undefined, right: ReadRouteEntry | undefined): boolean {
  return !!left && !!right && left.href === right.href && left.key === right.key;
}

/** A chooser exists only when the item really has more than one version. */
export function readVersionCanChoose(versions: readonly ReadVersion[]): boolean {
  return versions.length > 1;
}

/** The versions an owner may switch to: real, inventory-available and not the current one. */
export function readVersionAlternatives(versions: readonly ReadVersion[], chosen: string | undefined): readonly ReadVersion[] {
  return versions.filter(version => version.sourceId !== chosen && version.inventoryAvailable);
}

/** The exact version a selection may dispatch, or undefined when it is absent, duplicated,
 * unavailable or already the chosen one. It reads only the supplied eligibility list. */
export function readVersionPick(versions: readonly ReadVersion[], chosen: string | undefined, next: string | undefined): ReadVersion | undefined {
  if (!next || next === chosen) return undefined;
  const matches = versions.filter(version => version.sourceId === next);
  return matches.length === 1 && matches[0]!.inventoryAvailable ? matches[0] : undefined;
}

/** The eligibility facts a rendered chooser row was offered on and the committed
 * eligibility identity it must still match. */
export type ReadVersionSource = Readonly<{
  context: unknown;
  sourceId: string | undefined;
  versions: readonly ReadVersion[];
}>;

type ReadVersionSelection = ReadVersionSource & Readonly<{attempt: unknown}>;

/** The row itself must still be the exact, unique, available, non-current row. */
function readVersionPickable(versions: readonly ReadVersion[], sourceId: string | undefined, candidate: ReadVersion): boolean {
  if (sourceId === undefined || candidate.sourceId === sourceId || !candidate.inventoryAvailable) return false;
  const matches = versions.filter(version => version.sourceId === candidate.sourceId);
  return matches.length === 1 && matches[0] === candidate;
}

/** The callback's own captured eligibility must be the committed eligibility: a replaced
 * list (even with equal contents) invalidates the old row instead of comparing the
 * callback against its own closure. */
function readSameSource(rendered: ReadVersionSource, committed: ReadVersionSource | undefined): boolean {
  if (!committed) return false;
  if (rendered.context !== committed.context) return false;
  if (rendered.sourceId === undefined || rendered.sourceId !== committed.sourceId) return false;
  return rendered.versions === committed.versions;
}

export type ReadVersionPreRetire = Readonly<{
  liveRoute: boolean;
  route: ReadRouteEntry | undefined;
  committedRoute: ReadRouteEntry | undefined;
  currentRoute: ReadRouteEntry | undefined;
  rendered: ReadVersionSelection;
  committed: ReadVersionSource | undefined;
  currentAttempt: unknown;
  attemptLive: boolean;
  candidate: ReadVersion;
}>;

/** The callback's own rendered entry must still be the entry committed by the last
 * layout commit and the live router entry. Old callbacks therefore refuse after a new
 * `__TSR_key`, while callbacks from the new entry's render work. */
function readSameRouteWitness(facts: Readonly<{route: ReadRouteEntry | undefined; committedRoute: ReadRouteEntry | undefined; currentRoute: ReadRouteEntry | undefined}>): boolean {
  return readSameRouteEntry(facts.route, facts.committedRoute) && readSameRouteEntry(facts.route, facts.currentRoute);
}

/** Admission before retirement: the original route entry, context, source and eligibility
 * list are unchanged, and the row's own attempt is the real live attempt that is still
 * the current one. Recovery from a failed or not-ready attempt is allowed because its
 * liveness does not depend on `factsCurrent()`. */
export function readVersionPreRetire(facts: ReadVersionPreRetire): boolean {
  if (!facts.liveRoute || !readSameRouteWitness(facts)) return false;
  if (!facts.attemptLive || !facts.rendered.attempt || facts.rendered.attempt !== facts.currentAttempt) return false;
  if (!readSameSource(facts.rendered, facts.committed)) return false;
  return readVersionPickable(facts.rendered.versions, facts.rendered.sourceId, facts.candidate);
}

export type ReadVersionPostRetire = Readonly<{
  liveRoute: boolean;
  route: ReadRouteEntry | undefined;
  committedRoute: ReadRouteEntry | undefined;
  currentRoute: ReadRouteEntry | undefined;
  rendered: ReadVersionSelection;
  committed: ReadVersionSource | undefined;
  successor: unknown;
  candidate: ReadVersion;
}>;

/** Admission after retirement. The original attempt has been retired by definition, so
 * this deliberately does not require it to still be live or `attemptRef` current; it
 * requires the original route/context/source/list witness to be unchanged and no
 * successor to exist. */
export function readVersionPostRetire(facts: ReadVersionPostRetire): boolean {
  if (!facts.liveRoute || !readSameRouteWitness(facts)) return false;
  if (facts.successor !== undefined) return false;
  if (!readSameSource(facts.rendered, facts.committed)) return false;
  return readVersionPickable(facts.rendered.versions, facts.rendered.sourceId, facts.candidate);
}

/** The component's whole explicit recovery: pre-retire admission, retirement, then the
 * unchanged post-retire witness. `true` is the only result that permits the caller to
 * write the chosen source. */
export type ReadVersionRecovery = ReadVersionPreRetire & Readonly<{
  retire: (attempt: unknown) => void;
  afterRetire: () => Readonly<{liveRoute: boolean; committedRoute: ReadRouteEntry | undefined; currentRoute: ReadRouteEntry | undefined; successor: unknown; committed: ReadVersionSource | undefined}>;
}>;

export function readVersionRecovery(facts: ReadVersionRecovery): boolean {
  if (!readVersionPreRetire(facts)) return false;
  facts.retire(facts.rendered.attempt);
  const after = facts.afterRetire();
  return readVersionPostRetire({
    liveRoute: after.liveRoute, route: facts.route, committedRoute: after.committedRoute, currentRoute: after.currentRoute,
    rendered: facts.rendered, committed: after.committed, successor: after.successor, candidate: facts.candidate,
  });
}

/** Back's caller decision: the callback's own rendered entry must still be committed and
 * live, then `canGoBack` selects in-app Back or Home. An old callback returns undefined
 * after a new entry, while the new entry's callback decides normally. */
export function readBackCommit(route: ReadRouteEntry | undefined, committedRoute: ReadRouteEntry | undefined,
  currentRoute: ReadRouteEntry | undefined, canGoBack: boolean): 'back' | 'home' | undefined {
  if (!readSameRouteEntry(route, committedRoute) || !readSameRouteEntry(route, currentRoute)) return undefined;
  return readBackDestination(canGoBack);
}

/** An attempt that can be retired while abort callbacks are still registered. */
export type ReadRetirable = {live: boolean; ready: boolean; rereading?: boolean; target?: unknown};

/** Retirement must publish the attempt dead and detached before aborting: an abort
 * listener that reenters must never observe the retiring attempt as live or current. */
export function readRetireAttempt(attempt: ReadRetirable, detach: () => void, abort: () => void): void {
  attempt.live = false;
  attempt.ready = false;
  attempt.rereading = false;
  attempt.target = undefined;
  detach();
  abort();
}

/** Back returns in-app only when the captured route really has a predecessor. */
export function readBackDestination(canGoBack: boolean): 'back' | 'home' {
  return canGoBack ? 'back' : 'home';
}
