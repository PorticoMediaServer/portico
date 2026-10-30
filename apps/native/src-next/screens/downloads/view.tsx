import React, {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeEntryRoute} from '../../shell/routes';
import type {NativeShellRuntime} from '../../shell/runtime';
import {DownloadsScreen} from './DownloadsScreen';
import {createNativeDownloadsDevice} from './device';
import type {NativeOfflineDownloadsDevice, OfflineEntry} from '../../platform/offline-downloads';
import {openNativeReadingDetail, type NativeReadOrigin} from '../read/eligibility';
import {isReadingLibraryKind} from '../read/platform';
import type {NativeDownloadsCopy} from './presentation';

export const nativeDownloadsCopy = Object.freeze({
  emptyBody: 'next.saved.phone.emptyBody', storageLimitHelp: 'next.saved.phone.storageLimitHelp', freeOnPhone: 'next.saved.phone.free',
  removeFromPhone: 'next.saved.phone.remove', stoppedBody: 'next.saved.stopped', downloading: 'next.saved.downloading',
  needsCheckIn: 'next.saved.checkIn', goneFromServer: 'next.saved.phone.gone', otherProfiles: 'next.saved.phone.otherProfiles',
  stoppedAt: 'server.plays.stoppedAt', stoppedHelp: 'next.saved.phone.stoppedHelp',
} satisfies NativeDownloadsCopy);

/** The route instance this view actually rendered on, committed in layout. A
 * press passes the lease of the render that showed its entry; it never selects
 * a successor lease, and unmounting the view (pushed route or Saved tab) aborts it. */
type DownloadsLease = Readonly<{
  origin: NativeReadOrigin; sessionFamilyId: string; authority: ShellAuthority; signal: AbortSignal;
}>;

const downloadIdentity = (entry: OfflineEntry): string => JSON.stringify([entry.id, entry.itemId, entry.libraryId,
  entry.kind, entry.info?.kind, entry.owner, entry.receipt?.receiptId]);

function leaseValid(context: NativeShellBootstrapContext, lease: DownloadsLease): boolean {
  if (lease.signal.aborted || !context.current()) return false;
  const selected = context.session(), snapshot = context.navigation.getSnapshot();
  return !!selected && selected.sessionFamilyId === lease.sessionFamilyId
    && sameAuthority(lease.authority, context.authority())
    && snapshot.route === lease.origin.route && snapshot.revision === lease.origin.revision;
}

function DownloadsView({context, device, service, runtime, inline, back, onTitle}: Readonly<{
  context: NativeShellBootstrapContext; device: NativeOfflineDownloadsDevice; service: Parameters<typeof DownloadsScreen>[0]['service'];
  runtime: NativeShellRuntime; inline: boolean; back: () => void; onTitle: (entry: OfflineEntry, lease: DownloadsLease, runtime: NativeShellRuntime) => void;
}>) {
  const snapshot = useSyncExternalStore(context.navigation.subscribe, context.navigation.getSnapshot);
  const [lease, setLease] = useState<DownloadsLease | undefined>(undefined);
  useLayoutEffect(() => {
    const selected = context.session();
    // A lease belongs only to the surface this view is actually drawn on: the
    // pushed Downloads route, or Saved's own destination for the inline tab.
    // Any other route (a Settings page over a retained Saved) yields no lease
    // and the previous cleanup has already aborted the old one.
    const eligible = inline
      ? snapshot.route.kind === 'shell' && snapshot.route.destination.kind === 'saved'
      : snapshot.route.kind === 'downloads';
    if (!selected || !eligible) {setLease(undefined); return;}
    const abort = new AbortController();
    setLease(Object.freeze({
      origin: Object.freeze({route: snapshot.route, revision: snapshot.revision}),
      sessionFamilyId: selected.sessionFamilyId, authority: Object.freeze({...context.authority()}), signal: abort.signal,
    }));
    return () => {abort.abort();};
  }, [context, snapshot, inline]);
  return <DownloadsScreen device={device} service={service} runtime={runtime} copy={nativeDownloadsCopy} inline={inline}
    back={back} openTitle={entry => {if (lease) onTitle(entry, lease, runtime);}}/>;
}

/** The row's own title identity, checked against the lease of the render that
 * showed it. A downloaded publication opens the Reading book page; its item and
 * version come from the device's signed binding, never the cached label. */
function openNativeDownloadTitle(context: NativeShellBootstrapContext, device: NativeOfflineDownloadsDevice, entry: OfflineEntry, lease: DownloadsLease, runtime: NativeShellRuntime): Promise<void> | undefined {
  if (!leaseValid(context, lease)) return;
  const report = (error: unknown) => runtime.reportError(error);
  const live = device.getSnapshot().entries.find(value => value.id === entry.id && value.itemId === entry.itemId);
  const kind = live?.info?.kind ?? live?.kind;
  if (!live || !live.libraryId || !kind) {report(Object.assign(new Error('invalid_response'), {code: 'invalid_response'})); return;}
  if (kind === 'ebook' || kind === 'comic') {
    if (downloadIdentity(live) !== downloadIdentity(entry)) {report(Object.assign(new Error('invalid_response'), {code: 'invalid_response'})); return;}
    const library = context.libraries().find(value => value.id === live.libraryId);
    if (!library || !isReadingLibraryKind(library.kind)) {report(Object.assign(new Error('invalid_response'), {code: 'invalid_response'})); return;}
    let source: Readonly<{sourceId: string; revision: string}> | undefined;
    try {source = device.publicationSource?.(live);} catch (error) {if (leaseValid(context, lease)) report(error); return;}
    if (!source) {report(Object.assign(new Error('invalid_response'), {code: 'invalid_response'})); return;}
    return openNativeReadingDetail(context, lease.origin, {libraryId: live.libraryId, itemId: live.itemId, sourceId: source.sourceId,
      title: live.info?.title || live.title,
      stillCurrent: () => {
        if (!leaseValid(context, lease)) return false;
        const now = device.getSnapshot().entries.find(value => value.id === entry.id && value.itemId === entry.itemId);
        return !!now && downloadIdentity(now) === downloadIdentity(entry);
      }}, lease.signal)
      .then(ok => {if (ok || !leaseValid(context, lease)) return; report(Object.assign(new Error('source_unavailable'), {code: 'source_unavailable'}));},
        error => {if (leaseValid(context, lease)) report(error);});
  }
  if (!['movie', 'episode', 'song', 'audiobook_file', 'extra'].includes(kind)) {report(Object.assign(new Error('invalid_response'), {code: 'invalid_response'})); return;}
  if (kind === 'song' || kind === 'audiobook_file') {report(Object.assign(new Error('unavailable'), {code: 'unavailable'})); return;}
  // The descriptor uses the cached title's actual identity. The Title owner
  // reads its authorized detail; this does not synthesize a detail response.
  const title: ContentEntry = {id: live.itemId, libraryId: live.libraryId, kind: kind as ContentEntry['kind'], title: live.info?.title || live.title};
  context.navigation.open(nativeEntryRoute(title), context.authority());
  return undefined;
}

/** One real Downloads screen over the authority-local device, usable as the pushed
 * route or inside Saved's Downloads tab. The owner of this view retires its lease. */
export function createNativeDownloadsView(context: NativeShellBootstrapContext) {
  const bound = createNativeDownloadsDevice(context);
  let opening: object | undefined;
  const openTitle = (entry: OfflineEntry, lease: DownloadsLease, runtime: NativeShellRuntime) => {
    if (!leaseValid(context, lease)) return;
    const kind = entry.info?.kind ?? entry.kind;
    if (kind !== 'ebook' && kind !== 'comic') {openNativeDownloadTitle(context, bound.device, entry, lease, runtime); return;}
    if (opening) return;
    const ticket = {};
    opening = ticket;
    const pending = openNativeDownloadTitle(context, bound.device, entry, lease, runtime);
    if (!pending) {if (opening === ticket) opening = undefined; return;}
    void pending.finally(() => {if (opening === ticket) opening = undefined;});
  };
  return Object.freeze({
    render: (runtime: NativeShellRuntime, inline = false) => <DownloadsView context={context} device={bound.device} service={bound.service} runtime={runtime}
      inline={inline} back={() => {if (context.current()) context.navigation.back(context.authority());}} onTitle={openTitle}/>,
    dispose: bound.dispose,
  });
}
