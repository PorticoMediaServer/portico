import {readDownloadOptions} from '../../../../../../packages/client-core/src/device-downloads';
import {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {downloadBytes, downloadReasonMessage, parseDownloadOptions, type DownloadOptionsView} from '../../../../../../packages/client-core/src/downloads';
import type {ReadingClient} from '../../../../../../packages/client-core/src/reading';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {nativeRouteCrypto} from '../../../platform/server-connections';
import {createDownloadAttempts, type DownloadAttempts} from '../../../screens/downloads/download-attempt';
import {overlayPaintPermit} from '../../../kit/overlays/SurfacePaintHost';
import {createNativeDownloadsDevice} from '../../../screens/downloads/device';
import {getNativeDownloadSnapshot, closeNativeDownload, type NativeDownloadRequest} from '../../../screens/downloads/request';
import {nativeDestinationLifetime} from '../../../screens/destinations/lifetime';
import {linkNativeAbortSignals} from '../../../platform/abort-signals';
import {captureNativeDownloadPaintCurrent} from '../../../screens/downloads/download-opening';

/** `unreadable`: why the server cannot open this version, in its words; such a version offers no download. */
type Version = Readonly<{sourceId: string; format: string; size: number; inventoryAvailable: boolean; policy: DownloadOptionsView; unreadable?: string}>;
type Opening = {current: () => boolean; paintCurrent: () => boolean; retire: () => void; dispose: () => void; pending: boolean; attempts: DownloadAttempts; lease: ReturnType<typeof createNativeDownloadsDevice>; versions: readonly Version[]; abort: AbortController; signal: AbortSignal};
type Family = {retired: boolean; active?: Opening;
  /** Private: the exact context and request this family was constructed for. Reading them here
   * keeps the memo's original keys honest without publishing or comparing them anywhere else. */
  binding: Readonly<{context: NativeShellBootstrapContext; request: NativeDownloadRequest}>};
type State = Readonly<{opening?: Opening; versions?: readonly Version[]; policy?: DownloadOptionsView; busy?: string; error?: unknown}>;

/** The versions this device can read once downloaded (owner's ruling, 16 Nov: the app only downloads a
 * version it can read). A MOBI or AZW3 arrives as the EPUB the server makes from it, and a CBR, a CB7
 * or a folder of pictures as a CBZ. */
const readableOffline = (format: string) => ['epub', 'pdf', 'cbz', 'mobi', 'azw3', 'cbr', 'cb7', 'images'].includes(format);


/** The Download choice for a book, comic or magazine (tablet-read 12b): its versions this device can read, each with the
 * size of its original as the server's download choices give it, one chosen, then Download. No media-quality ladder. */
export function useReadingDownloadChoice({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const t = defaultI18n.t;
  const family = useMemo<Family>(() => ({retired: false, binding: {context, request}}), [context, request]);
  const rendered = useRef(family);
  if (rendered.current !== family) {rendered.current.retired = true; rendered.current.active?.retire(); rendered.current = family;}
  const [state, setState] = useState<State>({}), [retry, setRetry] = useState(0), [selected, setSelected] = useState<string>();
  useLayoutEffect(() => {
    const itemId = request.itemId;
    if (!itemId || !nativePublicationReadsAvailable()) return;
    const viewer = context.session()?.viewer; if (!viewer) return;
    const lifetime = nativeDestinationLifetime(context), route = context.navigation.getSnapshot();
    const paintCurrent = captureNativeDownloadPaintCurrent(context, request, lifetime.current);
    // Reuse the same resident request's first original binding; remounting in a
    // successor context must not reacquire Reading IO or an exit-paint permit.
    if (!paintCurrent()) {closeNativeDownload(request);return;}
    const abort = new AbortController(), lease = createNativeDownloadsDevice(context);
    const requestLifetime = request.lifetime;
    const linked = requestLifetime ? linkNativeAbortSignals([abort.signal, requestLifetime.signal]) : undefined;
    const signal = linked?.signal ?? abort.signal;
    let alive = true, disposed = false, client: ReadingClient | undefined; let offDevice = () => {};
    const current = () => alive && !family.retired && family.active === opening && rendered.current === family && !signal.aborted && lifetime.current()
      && paintCurrent()
      && (!requestLifetime || (!requestLifetime.signal.aborted && requestLifetime.isCurrent()))
      && getNativeDownloadSnapshot() === request && context.navigation.getSnapshot().revision === route.revision && context.navigation.getSnapshot().route === route.route;
    const stop = () => {if (!current()) abort.abort();};
    const closeOnRetire = () => {if (getNativeDownloadSnapshot() === request) closeNativeDownload(request);};
    requestLifetime?.signal.addEventListener('abort', closeOnRetire);
    const offAuthority = context.subscribeAuthority(stop), offRoute = context.navigation.subscribe(stop);
    const opening: Opening = {current, paintCurrent, pending: false, attempts: createDownloadAttempts(() => 'reading_' + Array.from(nativeRouteCrypto.random(16), byte => byte.toString(16).padStart(2, '0')).join('')), lease, versions: Object.freeze([]), abort, signal,
      retire: () => {if (!alive) return; alive = false; abort.abort(); linked?.dispose(); requestLifetime?.signal.removeEventListener('abort', closeOnRetire);},
      dispose: () => {opening.retire(); if (disposed) return; disposed = true; offAuthority(); offRoute(); offDevice(); client?.dispose(); lease.dispose();}};
    family.active = opening;
    offDevice = lease.device.subscribe(() => {if (current()) setState(value => value.opening === opening ? {...value} : value);});
    const reader = context.api.reading({viewer, isCurrent: current, signal});
    client = reader;
    setState({opening});
    void (async () => {
      const [versions] = await Promise.all([reader.versions(itemId, signal), lease.device.open()]);
      if (!current()) return;
      const supported = versions.versions.filter(value => readableOffline(value.format));
      const rows: Version[] = []; let next = 0;
      await Promise.all(Array.from({length: Math.min(4, supported.length)}, async () => {
        while (next < supported.length) {
          const index = next++, version = supported[index]; if (!version || !current()) return;
          const [raw, book] = await Promise.all([readDownloadOptions(context.api, itemId, {versionId: version.sourceId}, signal), reader.book(itemId, version.sourceId, signal)]);
          if (!current()) return;
          const policy = parseDownloadOptions(raw, itemId);
          rows[index] = Object.freeze({...version, size: policy.source.bytes, policy, ...(book.unreadable ? {unreadable: book.unreadable.message} : {})});
        }
      }));
      if (!current()) return;
      opening.versions = Object.freeze(rows); setState({opening, versions: opening.versions, policy: rows[0]?.policy});
    })().catch(error => {if (current()) setState({opening, error});});
    return () => {opening.dispose(); if (family.active === opening) family.active = undefined;};
  }, [context, request, family, retry]);
  const close = (opening: Opening | undefined) => {if (!opening) {if (!family.retired && family.active === undefined && context.current()) closeNativeDownload(request); return;} if (family.active !== opening || !opening.current()) return; opening.retire(); closeNativeDownload(request);};
  const choose = async (opening: Opening, version: Version) => {
    const policy = version.policy;
    if (!opening.current() || opening.pending || !opening.versions.includes(version) || !version.inventoryAvailable || !readableOffline(version.format) || version.unreadable !== undefined || !policy.policy.allowDownloads || !policy.source.available || !policy.options.some(value => value.quality === 'original' && value.available) || !Number.isSafeInteger(version.size) || version.size < 1 || version.size > 2 ** 31) return;
    const device = opening.lease.device.getSnapshot();
    if (!device.ready || opening.lease.device.admission?.(version.size) || version.size > device.freeBytes || policy.storage.remainingBytes !== null && policy.storage.remainingBytes !== undefined && version.size > policy.storage.remainingBytes) return;
    opening.pending = true; setState(value => value.opening === opening && opening.current() ? {...value, busy: version.sourceId, error: undefined} : value);
    try {
      // The same version tried again resends the identical body under the same id (download-attempt).
      const operationId = opening.attempts.operationFor(JSON.stringify([request.itemId, version.sourceId, 'original']));
      if (!opening.current()) return;
      const batch = await opening.lease.service.request({mediaId: request.itemId, versionId: version.sourceId}, 'original', operationId);
      opening.attempts.settle();
      if (!opening.current()) return;
      const accepted = batch.items.filter(value => value.itemId === request.itemId);
      if (!accepted.length) throw Object.assign(new Error(downloadReasonMessage(batch.rejected[0]?.code ?? '') || 'reading_unavailable'), {code: batch.rejected[0]?.code ?? 'reading_unavailable'});
      await opening.lease.want(accepted, opening.lease.display(accepted[0]!), opening.lease.service);
      if (opening.current()) close(opening);
    } catch (error) {
      if ((error as {code?: unknown} | undefined)?.code === 'idempotency_key_reused') opening.attempts.settle();
      if (opening.current()) setState(value => value.opening === opening && opening.current() ? {...value, error} : value);
    }
    finally {opening.pending = false; if (opening.current()) setState(value => value.opening === opening && opening.current() ? {...value, busy: undefined} : value);}
  };
  const shown: State = state.opening?.current() ? state : {};
  const blocked = shown.policy && !shown.policy.policy.allowDownloads;
  // tablet-read12b: the same versions and real preparation, chosen before the one
  // primary action. A side panel keeps the book visible on handheld tablets; the
  // phone keeps its sheet. No recommended flag is published, so the first version
  // the server lists as available is selected rather than claiming a recommendation.
  const versions = shown.versions?.filter(value => readableOffline(value.format)) ?? [];
  const opened = shown.opening;
  const ready = (version: Version) => {
    // A retained event must retire with its exact opening before touching the
    // borrowed device; a stale opening's lease is already released.
    if (!opened || !opened.current()) return false;
    const policy = version.policy, device = opened.lease.device.getSnapshot();
    return device.ready && !!policy && version.inventoryAvailable && version.unreadable === undefined && policy.policy.allowDownloads && policy.source.available
      && policy.options.some(value => value.quality === 'original' && value.available) && Number.isSafeInteger(version.size) && version.size > 0 && version.size <= 2 ** 31 && !shown.busy
      && !opened.lease.device.admission?.(version.size) && version.size <= device.freeBytes
      && (policy.storage.remainingBytes === undefined || policy.storage.remainingBytes === null || version.size <= policy.storage.remainingBytes);
  };
  // An explicit honest choice stays chosen even if it becomes unavailable; the
  // primary action then disables instead of silently switching the format.
  const selectedVersion = versions.find(value => value.sourceId === selected) ?? versions.find(ready) ?? versions[0];
  const opening = shown.opening, freeBytes = opening?.lease.device.getSnapshot().freeBytes;
  const admission = selectedVersion && opening?.current() ? opening.lease.device.admission?.(selectedVersion.size) : undefined;
  const paintCurrent = opened?.paintCurrent;
  const permit = paintCurrent ? overlayPaintPermit(paintCurrent,
    [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener)], request.lifetime ? [request.lifetime.signal] : []) : undefined;
  const loading = !shown.versions && shown.error === undefined && nativePublicationReadsAvailable();
  const unavailable = !nativePublicationReadsAvailable() || !!shown.versions && !versions.length;
  const refusal = blocked ? downloadReasonMessage(shown.policy!.policy.reason) || t('downloads.offForProfile') : admission || undefined;
  const failed = shown.error !== undefined;
  const busy = !!shown.busy;
  const retryLoad = () => {if (shown.opening?.current() && !shown.opening.pending) setRetry(value => value + 1);};
  const dismiss = () => close(shown.opening);
  const download = () => {if (opened && selectedVersion && ready(selectedVersion)) void choose(opened, selectedVersion);};
  const free = freeBytes === undefined ? undefined : downloadBytes(freeBytes);
  return {versions, selectedVersion, ready, select: setSelected, loading, unavailable, refusal, failed, busy, retryLoad, dismiss, download, free, permit,
    size: (version: Version) => downloadBytes(version.size)};
}
