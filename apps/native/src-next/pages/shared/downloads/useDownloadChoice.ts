import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {NativeModules, Platform} from 'react-native';
import {defaultI18n, type I18n} from '../../../../../../packages/i18n/src';
import {downloadBytes, downloadReasonMessage, type DownloadOptionsView, type DownloadQualityChoice} from '../../../../../../packages/client-core/src/downloads';
import type {DownloadEpisodes} from '../../../../../../packages/client-core/src/download-requests';
import type {ContainerDownloadPolicy} from '../../../../../../packages/client-core/src/downloads-service';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {overlayPaintPermit} from '../../../kit/overlays/SurfacePaintHost';
import {createNativeDownloadsDevice} from '../../../screens/downloads/device';
import {nativeDeviceSettings} from '../../../platform/device-settings';
import type {EngineSetting} from '../../../platform/playback/engine-choice';
import {originalMayNotPlay} from '../../../screens/downloads/original-opens';
import {closeNativeDownload, subscribeNativeDownload, type NativeDownloadRequest} from '../../../screens/downloads/request';

import {createNativeDownloadOpening, type NativeDownloadOpening} from '../../../screens/downloads/download-opening';
import {linkNativeAbortSignals} from '../../../platform/abort-signals';
import {containerDownloadBytes, downloadSheetMeaning, type ContainerDownloadOption} from '../../../screens/downloads/download-sheet-facts';
import {downloadRequestLines} from '../../../screens/downloads/presentation';
import {createDownloadAttempts, isDownloadQualityRequired, type DownloadAttempts} from '../../../screens/downloads/download-attempt';
import {nativeRuntimeRegionText} from '../../../screens/settings/runtime-region';
import {nativeRouteCrypto} from '../../../platform/server-connections';

/** `attempts`: this opening's submits; the same choice retried keeps its operation id (identical body), a changed one gets a new id. */
type SheetOpening = NativeDownloadOpening & Readonly<{lease: ReturnType<typeof createNativeDownloadsDevice>; deviceId?: string; attempts: DownloadAttempts}>;
const newOperationId = () => 'download_' + Array.from(nativeRouteCrypto.random(16), byte => byte.toString(16).padStart(2, '0')).join('');
const noSubscribe = (_listener: () => void) => () => {};

export function useDownloadChoice({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const regionText = useMemo(() => typeof context.preferences === 'function' && typeof context.current === 'function'
    ? nativeRuntimeRegionText(context) : undefined, [context]);
  useSyncExternalStore(typeof context.subscribePreferences === 'function' ? context.subscribePreferences : noSubscribe,
    () => typeof context.preferences === 'function' && (!context.current || context.current()) ? context.preferences() : undefined);
  const t = ((): I18n['t'] => {
    if (!regionText) return defaultI18n.t;
    try {return regionText.t;} catch {return defaultI18n.t;}
  })();
  const [opening, setOpening] = useState<SheetOpening>();
  useLayoutEffect(() => {
    const original = createNativeDownloadOpening(context, request);
    if (!original.current()) {original.close(); return;}
    const lease = createNativeDownloadsDevice(context), deviceId = context.session()?.deviceId;
    const value: SheetOpening = Object.freeze({...original, lease, deviceId, attempts: createDownloadAttempts(newOperationId)});
    const retire = () => {if (!original.current()) original.close();};
    const closeOnAbort = () => original.close();
    const offs = [context.navigation.subscribe(retire), context.subscribeAuthority(retire), subscribeNativeDownload(retire)];
    request.lifetime?.signal.addEventListener('abort', closeOnAbort, {once: true});
    if (original.current()) setOpening(value); else original.close();
    return () => {original.retire(); offs.forEach(off => off()); request.lifetime?.signal.removeEventListener('abort', closeOnAbort); lease.dispose();};
  }, [context, request]);
  const container = request.container, deviceId = opening?.deviceId;
  const [view, setView] = useState<DownloadOptionsView>(), [choices, setChoices] = useState<readonly ContainerDownloadOption[]>(), [policy, setPolicy] = useState<DownloadEpisodes>('all'), [keepNext, setKeepNext] = useState(1);
  const [selectionQualities, setSelectionQualities] = useState<readonly DownloadQualityChoice[]>();
  const [busy, setBusy] = useState(''), [done, setDone] = useState(''), [error, setError] = useState<unknown>();
  const [acceptedSingle, setAcceptedSingle] = useState(false);
  // The server asked for a quality (saved as Ask each time): the menu is read again and stays open, never an error.
  const [menuRead, setMenuRead] = useState(0);
  useSyncExternalStore(opening?.current() ? opening.lease.device.subscribe : noSubscribe,
    () => opening?.current() ? opening.lease.device.getSnapshot() : undefined);
  useEffect(() => {
    if (!opening?.current()) return;
    void opening.lease.device.open().catch(failure => {if (opening.current()) setError(failure);});
  }, [opening]);
  useEffect(() => {
    if (!opening?.current()) return;
    const abort = new AbortController(), linked = linkNativeAbortSignals([abort.signal, opening.signal]);
    const service = opening.lease.service;
    setError(undefined); setDone(''); setAcceptedSingle(false); setView(undefined); setChoices(undefined); setSelectionQualities(undefined);
    if (request.itemIds) {
      void service.qualities(linked.signal).then(next => {if (!linked.signal.aborted && opening.current()) setSelectionQualities(next);}, failure => {if (!linked.signal.aborted && opening.current()) setError(failure);});
    } else if (!container && request.itemId) {
      void service.options(request.itemId, linked.signal).then(next => {if (!linked.signal.aborted && opening.current()) setView(next);}, failure => {if (!linked.signal.aborted && opening.current()) setError(failure);});
    } else if (container && deviceId) {
      const wanted: ContainerDownloadPolicy = policy === 'next' ? {episodes: policy, keepNext} : {episodes: policy};
      void service.containerOptions({kind: container.kind, id: container.id, title: request.title}, deviceId, wanted, linked.signal)
        .then(next => {if (!linked.signal.aborted && opening.current()) setChoices(next.options);}, failure => {if (!linked.signal.aborted && opening.current()) setError(failure);});
    }
    return () => {abort.abort(); linked.dispose();};
    // The whole-request size follows the chosen policy, so each change reads it again.
  }, [opening, request, container, deviceId, policy, keepNext, menuRead]);
  const close = () => opening ? opening.close() : closeNativeDownload(request);
  const choose = async (quality: string) => {
    const ticket = opening?.claim();
    if (!opening || !ticket) return;
    const download = opening.lease, service = download.service, attempts = opening.attempts;
    setBusy(quality); setError(undefined); setAcceptedSingle(false);
    try {
      // Capture this opening before any asynchronous device or server work.
      await download.device.open();
      if (!opening.owns(ticket)) return;
      if (container) {
        if (!deviceId) throw Object.assign(new Error('downloads.noDevice'), {code: 'noDevice'});
        const wanted: ContainerDownloadPolicy = policy === 'next' ? {episodes: policy, keepNext} : {episodes: policy};
        const operationId = attempts.operationFor(JSON.stringify(['container', container.kind, container.id, deviceId, quality, wanted]));
        const followed = await service.requestContainer({target: {kind: container.kind, id: container.id, title: request.title}, deviceId, quality, policy: wanted, operationId});
        attempts.settle();
        // A captured membership total is not an accepted-title count. Reuse the
        // request's real preparation progress, including any failed members.
        if (opening.owns(ticket)) setDone(downloadRequestLines(followed).map(line =>
          line.key === 'downloads.requestProgress' ? t(line.key, line.values as {ready: number; total: number})
            : line.key === 'downloads.requestFailed' ? t(line.key, line.values as {failed: number}) : t(line.key)).join('; '));
        return;
      }
      if (!request.itemId && !request.itemIds) return;
      const target = request.itemIds ? {mediaIds: request.itemIds} : {mediaId: request.itemId};
      const batch = await service.request(target, quality, attempts.operationFor(JSON.stringify(['item', request.itemId ?? request.itemIds, quality])));
      attempts.settle();
      if (!opening.owns(ticket)) return;
      await download.want(batch.items, batch.items[0] ? download.display(batch.items[0]) : undefined, service);
      if (!opening.owns(ticket)) return;
      setAcceptedSingle(batch.accepted === 1 && batch.rejected.length === 0 && !batch.duplicate);
      setDone(batch.rejected.length
        ? batch.accepted ? t('downloads.addedMixed', {accepted: batch.accepted, rejected: batch.rejected.length, reason: downloadReasonMessage(batch.rejected[0].code)}) : downloadReasonMessage(batch.rejected[0].code) || t('downloads.nothingAdded')
        : !batch.accepted || batch.duplicate ? batch.items.length ? t('downloads.already') : t('downloads.nothingAdded') : t('downloads.willArrive'));
    } catch (failure) {
      // A refusal that settles the intent retires its id; anything else (no answer, a server error) keeps it, so trying
      // the same choice again resends the identical body and gets the first answer if it was admitted.
      const code = (failure as {code?: unknown} | undefined)?.code;
      if (isDownloadQualityRequired(failure) || code === 'idempotency_key_reused') attempts.settle();
      if (!opening.owns(ticket)) return;
      if (isDownloadQualityRequired(failure)) setMenuRead(value => value + 1); else setError(failure);
    }
    finally {if (opening.owns(ticket)) setBusy(''); opening.release(ticket);}
  };
  const engine = ((): EngineSetting | undefined => {try {const value = nativeDeviceSettings(context).read('playerEngine'); return value === 'native' || value === 'mpv' ? value : 'automatic';} catch {return undefined;}})();
  const cannotOpen = !!view && originalMayNotPlay(view.source.container, {platform: Platform.OS === 'ios' ? 'apple' : 'android',
    mpv: Platform.OS === 'android' && (NativeModules.PorticoMpvPlaybackModule as undefined | {available?: boolean})?.available === true, player: engine ?? 'automatic'});
  const blocked = view && !view.policy.allowDownloads;
  const storage = view?.storage.remainingBytes;
  // The episode question belongs to real shows and seasons; an album asks only for its files and a smaller copy.
  const episodic = container?.kind === 'show' || container?.kind === 'season';
  const paintCurrent = opening?.paintCurrent;
  const permit = paintCurrent ? overlayPaintPermit(paintCurrent, [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener)], request.lifetime ? [request.lifetime.signal] : []) : undefined;
  const options: DownloadQualityRow[] = [];
  const notes = (values: readonly (string | false | undefined)[]) => values.filter((value): value is string => !!value);
  const adding = (quality: string) => busy === quality ? t('downloads.adding') : undefined;
  if (view && !blocked) for (const option of view.options) {
    const meaning = downloadSheetMeaning(option, view.source);
    options.push({quality: option.quality, label: option.label, available: option.available,
      size: option.available ? option.estimated ? t('downloads.aboutSize', {size: downloadBytes(option.estimatedBytes)}) : downloadBytes(option.estimatedBytes) : downloadReasonMessage(option.reason ?? ''),
      notes: notes([option.available && opening?.current() ? opening.lease.device.admission?.(option.estimatedBytes) : undefined,
        meaning ? t(meaning) : undefined, option.available && option.requiresPreparation && t('downloads.serverPrepares'),
        option.kind === 'source' && cannotOpen && t('downloads.quality.originalMayNotPlay')]), meta: adding(option.quality)});
  }
  for (const option of selectionQualities ?? []) {
    options.push({quality: option.quality, label: option.label, available: true, meta: adding(option.quality)});
  }
  for (const option of choices ?? []) {
    const estimated = containerDownloadBytes(option);
    options.push({quality: option.quality, label: option.label, available: option.available && !!deviceId,
      size: estimated === undefined ? undefined : option.estimated ? t('downloads.aboutSize', {size: downloadBytes(estimated)}) : downloadBytes(estimated),
      notes: notes([option.available && opening?.current() && estimated !== undefined ? opening.lease.device.admission?.(option.estimatedBytes) : undefined,
        option.available && option.sameAsOriginal && t('quality.sameAsOriginal')]), meta: adding(option.quality)});
  }
  const editable = !!opening?.current() && !busy;
  const changePolicy = (value: DownloadEpisodes) => {if (opening?.current() && !busy) setPolicy(value);};
  const changeKeepNext = (value: number) => {if (opening?.current() && !busy) setKeepNext(value);};
  return {t, permit, close, choose, editable, policy, keepNext, changePolicy, changeKeepNext, options, episodic, done, acceptedSingle,
    problem: error === undefined ? undefined : downloadError(error, t),
    checking: !done && (container ? !!deviceId && !choices : !view && !selectionQualities) && error === undefined,
    refused: blocked && view ? downloadReasonMessage(view.policy.reason) || t('downloads.offForProfile') : undefined,
    missingDevice: !!container && !deviceId,
    storage: storage == null ? undefined : t('downloads.spaceLeft', {size: downloadBytes(storage)}),
    viewDownloads: () => {opening?.viewDownloads();}};
}

export type DownloadQualityRow = Readonly<{quality: string; label: string; available: boolean; size?: string; notes?: readonly string[]; meta?: string}>;
/** A real read failure keeps its catalogue wording; an unknown failure is not
 * dressed as a download refusal. */
function downloadError(error: unknown, t: I18n['t']) {
  const code = (error as {code?: unknown})?.code;
  if (code === 'noDevice') return {title: t('downloads.noDevice')};
  if (typeof code === 'string' && code) return {title: t('downloads.error.action'), body: downloadReasonMessage(code) || undefined};
  return {title: t('downloads.error.action'), body: error instanceof Error ? error.message : undefined};
}
