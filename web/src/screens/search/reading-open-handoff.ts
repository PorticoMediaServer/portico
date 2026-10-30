import {randomId} from '@core/random-id.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {router} from '../../router';

type Location = typeof router.state.location;
type Session = Pick<ReturnType<ShellAssemblyContext['session']>, 'sessionFamilyId' | 'viewer'>;
export type IndexedReadingTarget = Readonly<{itemId: string; sourceId: string; sourceRevision: string}>;
export type IndexedReadingReceiver = Readonly<{kind: 'accepted'; target: IndexedReadingTarget; current: () => boolean}>
  | Readonly<{kind: 'ordinary' | 'pending' | 'refused'}>;
export type IndexedReadingTransfer = Readonly<{finished: Promise<void>; cancel: () => void; releaseSource: () => void}>;
type Offer = {id: string; target: IndexedReadingTarget; current: () => boolean; authority: () => boolean; resolved: boolean; receiver?: Location;
  cancel: () => void; detach: () => void};
const offers = new WeakMap<ShellAssemblyContext, Offer>(), listeners = new Set<() => void>();
const markerField = 'porticoIndexedReadingOpening';
let version = 0;
const changed = () => {++version;for (const listener of listeners) listener();};
const sameSession = (left: Session, right: Session) => left.sessionFamilyId === right.sessionFamilyId
  && (['serverId', 'authority', 'accountId', 'profileId', 'role'] as const).every(key => left.viewer[key] === right.viewer[key]);
const refused = () => new DOMException('Indexed Reading opening changed', 'AbortError');
export const indexedReadingOpeningVersion = () => version;
export const subscribeIndexedReadingOpenings = (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};};
export function indexedReadingOpeningMarker(location: Location): string | undefined | null {
  const value = (location.state as Location['state'] & Readonly<{porticoIndexedReadingOpening?: unknown}>)[markerField];
  return value === undefined ? undefined : typeof value === 'string' && value ? value : null;
}
function matches(location: Location, offer: Offer) {
  return indexedReadingOpeningMarker(location) === offer.id && location.pathname === '/read/' + encodeURIComponent(offer.target.itemId)
    && location.search.sourceId === offer.target.sourceId;
}

/** Only an admitted gesture stages this private tuple. History gets an opaque
 * marker, never a hit, revision, excerpt, publication path or reader location. */
export function openIndexedReadingTarget(context: ShellAssemblyContext, target: IndexedReadingTarget, sourceCurrent: () => boolean): IndexedReadingTransfer {
  if (!context.isCurrent() || !sourceCurrent()) throw refused();
  offers.get(context)?.cancel();
  const session = context.session(), original: Session = {sessionFamilyId: session.sessionFamilyId, viewer: {...session.viewer}};
  let live = true, transferred = false, off = () => {};
  const authority = () => context.isCurrent() && sameSession(context.session(), original);
  const current = () => live && authority();
  const cancel = () => {
    if (!live) return;
    live = false;off();if (offers.get(context) === offer) offers.delete(context);changed();
  };
  const offer: Offer = {id: randomId(), target: Object.freeze({...target}), current, authority, resolved: false, cancel, detach: () => off()};
  offers.set(context, offer);
  off = router.subscribe('onBeforeNavigate', event => {
    if (!current() || transferred || !matches(event.toLocation, offer)) {cancel();return;}
    transferred = true;
  });
  changed();
  let navigation: Promise<void>;
  try {
    navigation = router.navigate({to: '/read/$itemId', params: {itemId: offer.target.itemId}, search: {sourceId: offer.target.sourceId},
      state: previous => ({...previous, [markerField]: offer.id})});
  } catch (error) {cancel();throw error;}
  const finished = navigation.then(() => {
    if (!current() || !transferred || !matches(router.state.location, offer)) {cancel();throw refused();}
    offer.resolved = true;changed();
  }, error => {cancel();throw error;});
  return Object.freeze({finished, cancel, releaseSource: () => {
    // The intended receiver may outlive the Search route that dispatched it.
    // An unrelated navigation still cancels through the captured router listener.
    if (!transferred) cancel();
  }});
}

/** Called only by the committed receiver opening. A marked URL by itself never
 * grants an exact open; replay can read only this immutable accepted tuple. */
export function receiveIndexedReadingTarget(context: ShellAssemblyContext, itemId: string, sourceId: string | undefined, location: Location): IndexedReadingReceiver {
  const marker = indexedReadingOpeningMarker(location);
  if (marker === undefined) return Object.freeze({kind: 'ordinary'});
  if (marker === null) return Object.freeze({kind: 'refused'});
  const offer = offers.get(context);
  if (!offer || !offer.current() || !matches(location, offer) || offer.target.itemId !== itemId || offer.target.sourceId !== sourceId
    || router.state.location !== location || offer.receiver && offer.receiver !== location) return Object.freeze({kind: 'refused'});
  if (!offer.resolved) return Object.freeze({kind: 'pending'});
  offer.receiver = location;offer.detach();
  return Object.freeze({kind: 'accepted', target: offer.target, current: offer.authority});
}

/** After admission the exact receiver owns route retirement. The weak immutable
 * receipt may serve that opening's effect replay without retaining a router listener. */
export function retireIndexedReadingOpening(context: ShellAssemblyContext, location: Location) {
  const offer = offers.get(context);
  if (offer?.receiver === location && indexedReadingOpeningMarker(location) === offer.id) offer.cancel();
}
