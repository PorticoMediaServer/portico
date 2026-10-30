import {NativeReadingDownloadSheet} from './ReadingDownloadSheet';
import {NativeQueryDownloadSheet} from '../library/SelectionActionSurface';
import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {NativeModules, Platform, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {downloadBytes, downloadReasonMessage, type DownloadOptionsView, type DownloadQualityOption} from '../../../../../packages/client-core/src/downloads';
import type {DownloadEpisodes} from '../../../../../packages/client-core/src/download-requests';
import type {ContainerDownloadPolicy} from '../../../../../packages/client-core/src/downloads-service';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {Focusable} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {BottomSheet} from '../../kit/overlays';
import {overlayPaintPermit, RetainedOverlay} from '../../kit/overlays/SurfacePaintHost';
import {StateView} from '../../kit/states';
import {createNativeDownloadsDevice} from './device';
import {nativeDeviceSettings} from '../../platform/device-settings';
import type {EngineSetting} from '../../platform/playback/engine-choice';
import {originalMayNotPlay} from './original-opens';
import {closeNativeDownload, getNativeDownloadSnapshot, nativeDownloadRequestKey, subscribeNativeDownload, type NativeDownloadRequest} from './request';

import {createNativeDownloadOpening, type NativeDownloadOpening} from './download-opening';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {containerDownloadBytes, downloadSheetMeaning, type ContainerDownloadOption} from './download-sheet-facts';
import {downloadRequestLines} from './presentation';

type SheetOpening = NativeDownloadOpening & Readonly<{lease: ReturnType<typeof createNativeDownloadsDevice>; deviceId?: string}>;
const noSubscribe = (_listener: () => void) => () => {};

/** The phone's drawn download sheet (collections 31a/32a-c): the server's real
 * quality menu, the whole-container episode policy, the sizes it will be, the
 * known server room, then today's confirmation. Device room stays unknown
 * until a measured storage fact is available. */
export function NativeDownloadSheet({context}: {context: NativeShellBootstrapContext}) {
  const request = useSyncExternalStore(subscribeNativeDownload, getNativeDownloadSnapshot);
  if (request?.queryAction) return <NativeQueryDownloadSheet key={nativeDownloadRequestKey(request)} action={request.queryAction} onClose={() => {closeNativeDownload(request);}}/>;
  return request?.reading ? <NativeReadingDownloadSheet context={context} request={request}/> : request ? <NativeDownloadSheetBody context={context} request={request} key={nativeDownloadRequestKey(request)}/> : null;
}

function NativeDownloadSheetBody({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [opening, setOpening] = useState<SheetOpening>();
  useLayoutEffect(() => {
    const original = createNativeDownloadOpening(context, request);
    if (!original.current()) {original.close(); return;}
    const lease = createNativeDownloadsDevice(context), deviceId = context.session()?.deviceId;
    const value: SheetOpening = Object.freeze({...original, lease, deviceId});
    const retire = () => {if (!original.current()) original.close();};
    const closeOnAbort = () => original.close();
    const offs = [context.navigation.subscribe(retire), context.subscribeAuthority(retire), subscribeNativeDownload(retire)];
    request.lifetime?.signal.addEventListener('abort', closeOnAbort, {once: true});
    if (original.current()) setOpening(value); else original.close();
    return () => {original.retire(); offs.forEach(off => off()); request.lifetime?.signal.removeEventListener('abort', closeOnAbort); lease.dispose();};
  }, [context, request]);
  const container = request.container, deviceId = opening?.deviceId;
  const [view, setView] = useState<DownloadOptionsView>(), [choices, setChoices] = useState<readonly ContainerDownloadOption[]>(), [policy, setPolicy] = useState<DownloadEpisodes>('all'), [keepNext, setKeepNext] = useState(1);
  const [busy, setBusy] = useState(''), [done, setDone] = useState(''), [error, setError] = useState<unknown>();
  const [acceptedSingle, setAcceptedSingle] = useState(false);
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
    setError(undefined); setDone(''); setAcceptedSingle(false); setView(undefined); setChoices(undefined);
    if (!container && request.itemId) {
      void service.options(request.itemId, linked.signal).then(next => {if (!linked.signal.aborted && opening.current()) setView(next);}, failure => {if (!linked.signal.aborted && opening.current()) setError(failure);});
    } else if (container && deviceId) {
      const wanted: ContainerDownloadPolicy = policy === 'next' ? {episodes: policy, keepNext} : {episodes: policy};
      void service.containerOptions({kind: container.kind, id: container.id, title: request.title}, deviceId, wanted, linked.signal)
        .then(next => {if (!linked.signal.aborted && opening.current()) setChoices(next.options);}, failure => {if (!linked.signal.aborted && opening.current()) setError(failure);});
    }
    return () => {abort.abort(); linked.dispose();};
    // The whole-request size follows the chosen policy, so each change reads it again.
  }, [opening, request, container, deviceId, policy, keepNext]);
  const close = () => opening ? opening.close() : closeNativeDownload(request);
  const choose = async (quality: string) => {
    const ticket = opening?.claim();
    if (!opening || !ticket) return;
    const download = opening.lease, service = download.service;
    setBusy(quality); setError(undefined); setAcceptedSingle(false);
    try {
      // Capture this opening before any asynchronous device or server work.
      await download.device.open();
      if (!opening.owns(ticket)) return;
      if (container) {
        if (!deviceId) throw Object.assign(new Error('downloads.noDevice'), {code: 'noDevice'});
        const followed = await service.requestContainer({target: {kind: container.kind, id: container.id, title: request.title}, deviceId, quality,
          policy: policy === 'next' ? {episodes: policy, keepNext} : {episodes: policy}});
        // A captured membership total is not an accepted-title count. Reuse the
        // request's real preparation progress, including any failed members.
        if (opening.owns(ticket)) setDone(downloadRequestLines(followed).map(line =>
          line.key === 'downloads.requestProgress' ? t(line.key, line.values as {ready: number; total: number})
            : line.key === 'downloads.requestFailed' ? t(line.key, line.values as {failed: number}) : t(line.key)).join('; '));
        return;
      }
      if (!request.itemId) return;
      const batch = await service.request({mediaId: request.itemId}, quality);
      if (!opening.owns(ticket)) return;
      await download.want(batch.items, batch.items[0] ? download.display(batch.items[0]) : undefined, service);
      if (!opening.owns(ticket)) return;
      setAcceptedSingle(batch.accepted === 1 && batch.rejected.length === 0 && !batch.duplicate);
      setDone(batch.rejected.length
        ? batch.accepted ? t('downloads.addedMixed', {accepted: batch.accepted, rejected: batch.rejected.length, reason: downloadReasonMessage(batch.rejected[0].code)}) : downloadReasonMessage(batch.rejected[0].code) || t('downloads.nothingAdded')
        : !batch.accepted || batch.duplicate ? batch.items.length ? t('downloads.already') : t('downloads.nothingAdded') : t('downloads.willArrive'));
    } catch (failure) {if (opening.owns(ticket)) setError(failure);}
    finally {if (opening.owns(ticket)) setBusy(''); opening.release(ticket);}
  };
  const policyRow = (id: DownloadEpisodes, label: string) => <Focusable key={id} testID={`download-policy-${id}`} accessibilityRole="button" accessibilityState={{selected: policy === id}} onPress={() => {if (opening?.current() && !busy) setPolicy(id);}} focusRadius={12}
    style={{minHeight: 48, paddingHorizontal: 16, flexDirection: 'row', alignItems: 'center', gap: 12}}>
    <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{label}</Text>
    {policy === id && <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/>}
  </Focusable>;
  const qualityRow = (option: Readonly<{quality: string; label: string; available: boolean; size?: string; notes?: readonly string[]; meta?: string}>) => <Focusable key={option.quality} testID={`download-quality-${option.quality}`} accessibilityRole="button"
    disabled={!opening?.current() || !option.available || !!busy} onPress={() => {void choose(option.quality);}} focusRadius={12}
    style={{minHeight: 56, paddingHorizontal: 16, paddingVertical: 8, flexDirection: 'row', alignItems: 'center', gap: 12, opacity: option.available ? 1 : .55}}>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{option.label}</Text>
      {(!!option.size || !!option.notes?.length) && <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: 10, marginTop: 2}}>
        {!!option.size && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{option.size}</Text>}
        {option.notes?.map((note, index) => <Text key={index} numberOfLines={3} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{note}</Text>)}
      </View>}
    </View>
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{option.meta ?? (option.available ? t('action.download') : '')}</Text>
  </Focusable>;
  const titleOption = (option: DownloadQualityOption) => {
    const meaning = view && downloadSheetMeaning(option, view.source);
    const admission = option.available && opening?.current() ? opening.lease.device.admission?.(option.estimatedBytes) : undefined;
    return qualityRow({quality: option.quality, label: option.label, available: option.available,
      size: option.available ? option.estimated ? t('downloads.aboutSize', {size: downloadBytes(option.estimatedBytes)}) : downloadBytes(option.estimatedBytes) : downloadReasonMessage(option.reason ?? ''),
      notes: [admission || undefined, meaning ? t(meaning) : undefined, option.available && option.requiresPreparation ? t('downloads.serverPrepares') : undefined, option.kind === 'source' && cannotOpen ? t('downloads.quality.originalMayNotPlay') : undefined].filter((value): value is string => !!value),
      meta: busy === option.quality ? t('downloads.adding') : undefined});
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
  return <RetainedOverlay permit={permit} allowedTypes={[BottomSheet, React.Fragment, View, Text, Focusable, StateView]}><BottomSheet open onOpenChange={open => {if (!open) close();}} testID="native-download-sheet" title={t(done ? 'action.download' : 'downloads.chooseQuality')} description={request.title} fitContent
    footer={done ? <View style={{flexDirection: 'row', gap: 12}}><Focusable accessibilityRole="button" onPress={close} focusRadius={12} style={{minHeight: 48, paddingHorizontal: 16, alignItems: 'center', justifyContent: 'center'}}><Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{t('action.close')}</Text></Focusable>
      <Focusable accessibilityRole="button" onPress={() => {opening?.viewDownloads();}} focusRadius={12} style={{minHeight: 48, paddingHorizontal: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.text, borderRadius: 12}}><Text style={[theme.type.bodyStrong, {color: theme.colors.ink}]}>{t('downloads.view')}</Text></Focusable></View> : undefined}>
    <View style={{gap: 16}}>
      {error !== undefined && <StateView {...{...downloadError(error), tone: 'error' as const}}/>}
      {done ? <>{acceptedSingle ? <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('downloads.addedMany', {count: 1})}</Text> : null}<Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{done}</Text></> : <>
        {blocked && view && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{downloadReasonMessage(view.policy.reason) || t('downloads.offForProfile')}</Text>}
        {container && !deviceId && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('downloads.noDevice')}</Text>}
        {episodic && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{policyRow('all', t('downloads.policyAll'))}{policyRow('unwatched', t('downloads.policyUnwatched'))}{policyRow('next', t('downloads.policyNext'))}</View>}
        {episodic && policy === 'next' && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{[1, 3, 5].map(count => <Focusable key={count} accessibilityRole="button" accessibilityState={{selected: keepNext === count}} onPress={() => {if (opening?.current() && !busy) setKeepNext(count);}} focusRadius={12}
          style={{minHeight: 48, paddingHorizontal: 16, flexDirection: 'row', alignItems: 'center'}}><Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{t('downloads.keepNext', {count})}</Text>{keepNext === count && <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/>}</Focusable>)}</View>}
        {!done && !container && !view && error === undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('downloads.checking')}</Text>}
        {view && !blocked && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{view.options.map(titleOption)}</View>}
        {container && choices && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{choices.map(option => qualityRow({quality: option.quality, label: option.label, available: option.available && !!deviceId,
          size: containerDownloadBytes(option) === undefined ? undefined : option.estimated ? t('downloads.aboutSize', {size: downloadBytes(option.estimatedBytes)}) : downloadBytes(option.estimatedBytes),
          notes: [option.available && opening?.current() && containerDownloadBytes(option) !== undefined ? opening.lease.device.admission?.(option.estimatedBytes) : undefined,
            option.available && option.sameAsOriginal ? t('quality.sameAsOriginal') : undefined].filter((value): value is string => !!value), meta: busy === option.quality ? t('downloads.adding') : undefined}))}</View>}
        {storage != null && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('downloads.spaceLeft', {size: downloadBytes(storage)})}</Text>}
      </>}
    </View>
  </BottomSheet></RetainedOverlay>;
}

/** A real read failure keeps its catalogue wording; an unknown failure is not
 * dressed as a download refusal. */
function downloadError(error: unknown) {
  const code = (error as {code?: unknown})?.code;
  if (code === 'noDevice') return {title: defaultI18n.t('downloads.noDevice')};
  if (typeof code === 'string' && code) return {title: defaultI18n.t('downloads.error.action'), body: downloadReasonMessage(code) || undefined};
  return {title: defaultI18n.t('downloads.error.action'), body: error instanceof Error ? error.message : undefined};
}
