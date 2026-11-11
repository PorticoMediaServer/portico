import type {LocalSession} from '@core/index.ts';

export type LibraryRouteProofSnapshot = Readonly<{serverId: string; libraryIds: readonly string[]}>;
/** One current proof, never Home-row membership or the tolerant display projection. */
export type LibraryRouteProof = Readonly<{
  getSnapshot(): LibraryRouteProofSnapshot | undefined;
  isCurrent(): boolean;
  subscribe(retire: () => void, afterRetire?: () => void): () => void;
  request(): void;
  reconcile(): void;
  claim(snapshot: LibraryRouteProofSnapshot, opening: object): boolean;
}>;
type Session = Pick<LocalSession, 'sessionFamilyId' | 'viewer' | 'deviceId'>;
type Version = string | number;
type Candidate = Readonly<{ids: readonly string[]; version: Version}>;
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);

/** Validate completeness separately: a dropped/duplicate/malformed row cannot prove absence.
 * Fixed entry/byte bounds are conservative nonadoption, never eviction of a live route. */
export function completeLibraryDirectory(raw: unknown): readonly string[] | undefined {
  if (!record(raw) || !Array.isArray(raw.items) || raw.items.length > 2048 || raw.hasMore === true || raw.nextCursor) return;
  const ids: string[] = [], seen = new Set<string>();let bytes = 0;
  for (const row of raw.items) {
    if (!record(row) || !text(row.id, 256) || !text(row.name, 8192) || typeof row.kind !== 'string' || row.kind.length > 256 || seen.has(row.id)) return;
    bytes += new TextEncoder().encode(row.id).byteLength;
    if (bytes > 512 * 1024) return;
    seen.add(row.id);ids.push(row.id);
  }
  return Object.freeze(ids);
}

/** The library directory's own proof (client data step 5): its complete list of libraries and the
 * viewer fence it answered with, read in one response. Home is not involved. */
export function createLibraryRouteProof(o: Readonly<{
  session(): Session; isCurrent(): boolean; directoryVersion(): Version; request(): void;
}>) {
  const initial = o.session(), family = initial.sessionFamilyId, device = initial.deviceId, viewer = Object.freeze({...initial.viewer});
  let closed = false, directoryTicket: Readonly<{wave: object; version: Version}> | undefined, candidate: Candidate | undefined;
  let directoryWave: object = {};
  let snapshot: LibraryRouteProofSnapshot | undefined, claimedOpening: object | undefined;
  const listeners = new Set<Readonly<{retire(): void; afterRetire?: () => void}>>();
  const current = () => {
    if (closed || !o.isCurrent()) return false;
    const now = o.session();
    return now.sessionFamilyId === family && now.deviceId === device
      && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => now.viewer[key] === viewer[key]) && !closed && o.isCurrent();
  };
  const inputsCurrent = () => !!candidate && candidate.version === o.directoryVersion();
  const getSnapshot = () => {
    const value = snapshot;
    return current() && inputsCurrent() && current() && value === snapshot ? value : undefined;
  };
  // Every original route guard retires before any replacement navigation runs.
  const reconcile = () => {const wave = [...listeners];for (const listener of wave) listener.retire();for (const listener of wave) listener.afterRetire?.();};
  const invalidate = () => {directoryWave = {};directoryTicket = undefined;candidate = undefined;snapshot = undefined;reconcile();};
  const publish = () => {
    if (!current() || !inputsCurrent()) return false;
    snapshot = Object.freeze({serverId: viewer.serverId, libraryIds: candidate!.ids});
    reconcile();return true;
  };
  const source: LibraryRouteProof = Object.freeze({getSnapshot, reconcile, isCurrent: current,
    subscribe(retire, afterRetire) {if (closed) return () => {};const listener = {retire, afterRetire};listeners.add(listener);return () => {listeners.delete(listener);};},
    request() {if (!current()) return;invalidate();if (current()) o.request();},
    claim(value, opening) {
      if (getSnapshot() !== value || claimedOpening === opening) return false;
      claimedOpening = opening;return getSnapshot() === value;
    },
  });
  return {
    source, invalidate,
    beginDirectory() {
      directoryTicket = undefined;candidate = undefined;snapshot = undefined;reconcile();
      const ticket = {wave: directoryWave, version: o.directoryVersion()};
      if (current()) directoryTicket = ticket;return ticket;
    },
    directory(ticket: object, raw: unknown) {
      if (!current() || directoryTicket !== ticket || directoryTicket.wave !== directoryWave || directoryTicket.version !== o.directoryVersion()) return false;
      const ids = completeLibraryDirectory(raw);
      if (!ids || !current() || directoryTicket !== ticket || directoryTicket.version !== o.directoryVersion()) return false;
      candidate = {ids, version: directoryTicket.version};publish();return true;
    },
    dispose() {if (closed) return;closed = true;snapshot = undefined;candidate = undefined;directoryTicket = undefined;claimedOpening = undefined;reconcile();listeners.clear();},
  };
}
export type LibraryRouteProofOwner = ReturnType<typeof createLibraryRouteProof>;
export type LibraryRouteDestination = Readonly<{to: '/libraries' | '/'; replace: true}>;

// The route's library is no longer this viewer's (or its proof retired): readers stop at once and release
// nothing more. Any other end of the route (leaving it, unmounting) aborts with the default reason.
const accessEndedReason = new DOMException('Library access ended', 'AbortError');
export const libraryAccessEnded = (signal: AbortSignal | undefined): boolean => !!signal?.aborted && signal.reason === accessEndedReason;

/** One original route's actions and requests. It can only lose authority.
 * An item/reader supplies a successful current document to identify its parent. */
export function createLibraryRouteGuard(o: Readonly<{
  source?: LibraryRouteProof; opening: object; isCurrent(): boolean; canReplace(): boolean;
  libraryId?: string; readParent?: (signal: AbortSignal) => Promise<string>;
  replace(destination: LibraryRouteDestination): unknown; reportError(error: unknown): void;
}>) {
  const controller = new AbortController();
  let closed = false, retired = false, started = false, off: (() => void) | undefined;
  let pending: Readonly<{proof: LibraryRouteProofSnapshot; controller: AbortController}> | undefined;
  let attempted: LibraryRouteProofSnapshot | undefined, parent: Readonly<{proof: LibraryRouteProofSnapshot; libraryId: string}> | undefined;
  let departure: LibraryRouteProofSnapshot | undefined, checkedLibrary: LibraryRouteProofSnapshot | undefined, failure: unknown;
  const originalCurrent = () => !closed && o.source?.isCurrent() !== false && o.isCurrent() && !closed && o.source?.isCurrent() !== false;
  const stopRead = () => {const old = pending;pending = undefined;old?.controller.abort();};
  const retire = (reason?: unknown) => {if (retired) return;retired = true;stopRead();controller.abort(reason);};
  const isCurrent = () => {
    if (closed || retired) return false;
    if (!originalCurrent()) {retire(o.source?.isCurrent() === false ? accessEndedReason : undefined);return false;}
    const proof = o.libraryId ? o.source?.getSnapshot() : undefined;
    if (proof && checkedLibrary !== proof) {
      checkedLibrary = proof;
      if (!proof.libraryIds.includes(o.libraryId!) && originalCurrent() && o.source?.getSnapshot() === proof) {departure = proof;retire(accessEndedReason);}
    }
    return !retired && !closed;
  };
  const flush = () => {
    const value = departure;
    if (!value || !originalCurrent() || !o.canReplace() || !originalCurrent() || !o.source?.claim(value, o.opening) || !originalCurrent() || !o.canReplace()) return;
    departure = undefined;
    try {void Promise.resolve(o.replace({to: value.libraryIds.length ? '/libraries' : '/', replace: true})).catch(error => {if (originalCurrent()) o.reportError(error);});}
    catch (error) {if (originalCurrent()) o.reportError(error);}
  };
  const check = () => {
    if (!isCurrent()) return;
    const proof = o.source?.getSnapshot();
    if (!proof) {stopRead();return;}
    if (pending && pending.proof !== proof) stopRead();
    const libraryId = o.libraryId ?? (parent?.proof === proof ? parent.libraryId : undefined);
    if (libraryId) {
      if (!proof.libraryIds.includes(libraryId) && originalCurrent() && o.source?.getSnapshot() === proof && isCurrent()) {
        departure = proof;retire(accessEndedReason);
      }
      return;
    }
    if (!o.readParent || attempted === proof) return;
    attempted = proof;const read = pending = {proof, controller: new AbortController()};
    void Promise.resolve().then(() => {
      if (!isCurrent() || pending !== read || o.source?.getSnapshot() !== proof) return;
      return o.readParent!(read.controller.signal);
    }).then(library => {
      if (!isCurrent() || pending !== read || read.controller.signal.aborted || o.source?.getSnapshot() !== proof) return;
      if (!text(library, 256)) throw new Error('Invalid route parent library');
      pending = undefined;failure = undefined;parent = {proof, libraryId: library};o.source.reconcile();
    }).catch(error => {if (isCurrent() && pending === read && !read.controller.signal.aborted) {pending = undefined;failure = error;}});
  };
  return Object.freeze({signal: controller.signal, isCurrent, getFailure: () => failure,
    start() {
      if (started || closed) return;started = true;
      off = o.source?.subscribe(check, flush);
      if (!isCurrent()) {flush();return;}
      o.source?.reconcile();
      if (isCurrent() && !o.source?.getSnapshot()) o.source?.request();
    },
    dispose() {if (closed) return;closed = true;off?.();off = undefined;stopRead();parent = undefined;attempted = undefined;departure = undefined;checkedLibrary = undefined;failure = undefined;retire();},
  });
}
export type LibraryRouteGuard = ReturnType<typeof createLibraryRouteGuard>;
