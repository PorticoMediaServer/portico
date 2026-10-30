import type {ReadingHostContentsTarget} from '../../../../../packages/client-core/src/reading-host';
import {ReadingError, type ReadingClient, type ReadingItem} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
export type NativeReadEligibility = Awaited<ReturnType<ReadingClient['versions']>>;
export type NativeReadNavigationContext = Pick<NativeShellBootstrapContext, 'api' | 'session' | 'current' | 'navigation' | 'authority' | 'libraries'>;
/** The exact route and revision a page was laid out on. A retained page never
 * adopts a later snapshot: every helper and controller refuses a replaced origin. */
export type NativeReadOrigin = Readonly<{route: NativeShellRoute; revision: number}>;
/** Capture in a layout effect for the route being rendered. */
export function nativeReadOrigin(context: NativeReadNavigationContext, route: NativeShellRoute): NativeReadOrigin | undefined {
  if (!context.current()) return undefined;
  const snapshot = context.navigation.getSnapshot();
  return snapshot.route === route ? Object.freeze({route, revision: snapshot.revision}) : undefined;
}
const originCurrent = (context: NativeReadNavigationContext, origin: NativeReadOrigin): boolean => {
  if (!context.current()) return false;
  const snapshot = context.navigation.getSnapshot();
  return snapshot.route === origin.route && snapshot.revision === origin.revision;
};
/** Authoritative producer only. A book-labelled audiobook is never inferred as Read. */
export async function nativeReadEligibility(client: ReadingClient, itemId: string, signal?: AbortSignal): Promise<NativeReadEligibility | null> {
  try {const result = await client.versions(itemId, signal); return result.versions.length ? result : null;}
  catch (error) {if (error instanceof ReadingError && error.status === 404) return null; throw error;}
}
/** The caller supplies an actual selected version. There is no format/default
 * preference, inventory fallback, URL, or server credential in a route. */
export async function openNativeRead(context: NativeReadNavigationContext, target: Readonly<{itemId: string; sourceId: string; expectedSourceRevision?: string; contentsTarget?: ReadingHostContentsTarget}>, signal?: AbortSignal, origin?: NativeReadOrigin): Promise<boolean> {
  const selected = context.session(), snapshot = context.navigation.getSnapshot();
  if (origin && (snapshot.route !== origin.route || snapshot.revision !== origin.revision)) return false;
  // Root54: a supplied expectation is never downgraded to "no expectation", which
  // would open whatever the source is now. An empty one is refused before any
  // session exists; anything else is carried to the host's own revision proof.
  const expected = target.expectedSourceRevision;
  if (expected !== undefined && expected.length === 0) return false;
  const destination: NativeShellRoute = {kind: 'read', itemId: target.itemId, sourceId: target.sourceId,
    ...(expected !== undefined ? {expectedSourceRevision: expected} : {}),
    ...(target.contentsTarget ? {contentsTarget: target.contentsTarget} : {})};
  if (target.contentsTarget && (target.contentsTarget.sourceId !== target.sourceId || !target.contentsTarget.sourceRevision
    || !Number.isInteger(target.contentsTarget.index) || target.contentsTarget.index < 0 || target.contentsTarget.index > 511)) return false;
  if (signal?.aborted || !selected || !context.current() || !context.navigation.allows(destination)) return false;
  const current = () => origin ? originCurrent(context, origin) && context.session()?.sessionFamilyId === selected.sessionFamilyId
    : context.current() && context.session()?.sessionFamilyId === selected.sessionFamilyId && context.navigation.getSnapshot() === snapshot;
  const client = context.api.reading({viewer: selected.viewer, isCurrent: current, signal});
  try {
    const eligibility = await nativeReadEligibility(client, target.itemId, signal);
    if (!current() || !eligibility?.versions.some(version => version.sourceId === target.sourceId && version.inventoryAvailable)) return false;
    return context.navigation.open(destination, context.authority());
  } finally {client.dispose();}
}
/** The phone's drawn book destination: a visible, inventory-available Reading
 * item of this library, whose exact version the signed producer still offers.
 * The action is checked against the origin captured when the page was laid out. */
export async function openNativeReadBook(context: NativeReadNavigationContext, target: Readonly<{libraryId: string; item: ReadingItem; stillCurrent?: () => boolean}>, origin: NativeReadOrigin, signal?: AbortSignal): Promise<boolean> {
  const selected = context.session(), item = target.item;
  const stillCurrent = () => {try {return target.stillCurrent ? target.stillCurrent() : true;} catch {return false;}};
  if (signal?.aborted || !selected || !context.current() || !originCurrent(context, origin) || !stillCurrent()) return false;
  if (item.libraryId !== target.libraryId || item.inventoryAvailable !== true || !item.id || !item.sourceId) return false;
  const destination: NativeShellRoute = {kind: 'read-book', libraryId: target.libraryId, item};
  if (!context.navigation.allows(destination)) return false;
  const current = () => originCurrent(context, origin) && context.session()?.sessionFamilyId === selected.sessionFamilyId && stillCurrent();
  const client = context.api.reading({viewer: selected.viewer, isCurrent: current, signal});
  try {
    const eligibility = await nativeReadEligibility(client, item.id, signal);
    // The page may open on any real available version; it selects the item's
    // own source, or the first available one, and the reader door revalidates
    // that exact source again before opening its host session.
    if (!current() || !eligibility?.versions.some(version => version.inventoryAvailable)) return false;
    return context.navigation.open(destination, context.authority());
  } finally {client.dispose();}
}
/** The Downloads surface's "Go to title" for a downloaded publication. The item
 * identity and version come from the device's signed publication binding; the
 * page's own start metadata comes from one authorized preview session, which is
 * closed before the Reading route is admitted. Nothing is inferred from the
 * cached download label, and a preview that cannot be retired is never ready. */
export async function openNativeReadingDetail(context: NativeReadNavigationContext, origin: NativeReadOrigin, target: Readonly<{libraryId: string; itemId: string; sourceId: string; title: string; stillCurrent?: () => boolean}>, signal?: AbortSignal): Promise<boolean> {
  const selected = context.session();
  const suppliedCurrent = () => {try {return target.stillCurrent ? target.stillCurrent() : true;} catch {return false;}};
  if (signal?.aborted || !selected || !context.current() || !originCurrent(context, origin) || !suppliedCurrent()) return false;
  const current = () => !signal?.aborted && originCurrent(context, origin) && context.session()?.sessionFamilyId === selected.sessionFamilyId && suppliedCurrent();
  const client = context.api.reading({viewer: selected.viewer, isCurrent: current, signal});
  try {
    const session = await client.open({itemId: target.itemId, sourceId: target.sourceId}, signal);
    let item: ReadingItem | undefined, closeError: unknown;
    try {
      const manifest = session.manifest;
      if (manifest.itemId !== target.itemId || manifest.sourceId !== target.sourceId) return false;
      const series = manifest.metadata.series;
      item = Object.freeze({
        id: target.itemId, libraryId: target.libraryId, title: manifest.metadata.title || target.title,
        format: manifest.format, sourceId: target.sourceId, inventoryAvailable: true as const,
        ...(series && series.libraryId === target.libraryId && /^[A-Za-z0-9_-]{43}$/.test(series.id)
          ? {series: Object.freeze({id: series.id, name: series.name, source: series.source, ...(series.number === undefined ? {} : {number: series.number})})} : {}),
      });
    } finally {try {await session.close();} catch (error) {closeError = error;}}
    // A preview that could not be retired must not become an admitted route.
    if (closeError) throw closeError;
    if (!item || !current()) return false;
    return openNativeReadBook(context, {libraryId: target.libraryId, item, stillCurrent: suppliedCurrent}, origin, signal);
  } finally {client.dispose();}
}
/** The series page only admits a signed series of this library that currently
 * returns real members; a label hash, a stale id or an unready series cannot open it. */
export async function openNativeReadSeries(context: NativeReadNavigationContext, target: Readonly<{libraryId: string; seriesId: string}>, origin: NativeReadOrigin, signal?: AbortSignal): Promise<boolean> {
  const selected = context.session();
  if (signal?.aborted || !selected || !context.current() || !originCurrent(context, origin) || !/^[A-Za-z0-9_-]{43}$/.test(target.seriesId)) return false;
  const destination: NativeShellRoute = {kind: 'read-series', libraryId: target.libraryId, seriesId: target.seriesId};
  if (!context.navigation.allows(destination)) return false;
  const current = () => originCurrent(context, origin) && context.session()?.sessionFamilyId === selected.sessionFamilyId;
  const client = context.api.reading({viewer: selected.viewer, isCurrent: current, signal});
  try {
    const page = await client.series(target.libraryId, target.seriesId, {limit: 1}, signal).catch(error => {
      if (error instanceof ReadingError && (error.status === 404 || error.code === 'not_found')) return undefined;
      throw error;
    });
    if (!page?.items.length || !current()) return false;
    return context.navigation.open(destination, context.authority());
  } finally {client.dispose();}
}
/** A magazine or newspaper group is admitted only for a current book library
 * whose signed periodical producer returns real issues for this exact group. */
export async function openNativeReadPeriodical(context: NativeReadNavigationContext, target: Readonly<{libraryId: string; groupId: string; publicationType: 'magazine' | 'newspaper'}>, origin: NativeReadOrigin, signal?: AbortSignal): Promise<boolean> {
  const selected = context.session();
  if (signal?.aborted || !selected || !context.current() || !originCurrent(context, origin) || !/^[A-Za-z0-9_-]{43}$/.test(target.groupId)) return false;
  const library = context.libraries().find(value => value.id === target.libraryId);
  if (!library || library.kind !== 'book') return false;
  const destination: NativeShellRoute = {kind: 'read-periodical', libraryId: target.libraryId, groupId: target.groupId, publicationType: target.publicationType};
  if (!context.navigation.allows(destination)) return false;
  const current = () => originCurrent(context, origin) && context.session()?.sessionFamilyId === selected.sessionFamilyId;
  const client = context.api.reading({viewer: selected.viewer, isCurrent: current, signal});
  try {
    const page = await client.periodicalIssues(target.libraryId, target.groupId, target.publicationType, {limit: 1}, signal).catch(error => {
      if (error instanceof ReadingError && (error.status === 404 || error.code === 'not_found')) return undefined;
      throw error;
    });
    if (!page?.items.length || !current()) return false;
    return context.navigation.open(destination, context.authority());
  } finally {client.dispose();}
}
