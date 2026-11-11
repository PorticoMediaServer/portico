import {NativeReadingDownloadSheet} from './ReadingDownloadSheet';
import {NativeQueryDownloadSheet} from '../library/SelectionActionSurface';
import React, {useEffect, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {downloadBytes} from '../../../../../packages/client-core/src/downloads';
import {downloadsOff, readDownloadOptions} from '../../../../../packages/client-core/src/device-downloads';
import type {DownloadChoice, DownloadOptions} from '../../../../../packages/contracts/src/next/api';
import type {DownloadEpisodes} from '../../../../../packages/client-core/src/download-requests';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {Focusable} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {BottomSheet} from '../../kit/overlays';
import {overlayPaintPermit, RetainedOverlay} from '../../kit/overlays/SurfacePaintHost';
import {StateView} from '../../kit/states';
import {createNativeDownloadsDevice} from './device';
import {closeNativeDownload, getNativeDownloadSnapshot, nativeDownloadRequestKey, subscribeNativeDownload, type NativeDownloadRequest} from './request';

import {createNativeDownloadOpening, type NativeDownloadOpening} from './download-opening';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {downloadSheetMeaning} from './download-sheet-facts';

type SheetOpening = NativeDownloadOpening & Readonly<{lease: ReturnType<typeof createNativeDownloadsDevice>}>;
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
    const lease = createNativeDownloadsDevice(context);
    const value: SheetOpening = Object.freeze({...original, lease});
    const retire = () => {if (!original.current()) original.close();};
    const closeOnAbort = () => original.close();
    const offs = [context.navigation.subscribe(retire), context.subscribeAuthority(retire), subscribeNativeDownload(retire)];
    request.lifetime?.signal.addEventListener('abort', closeOnAbort, {once: true});
    if (original.current()) setOpening(value); else original.close();
    return () => {original.retire(); offs.forEach(off => off()); request.lifetime?.signal.removeEventListener('abort', closeOnAbort); lease.dispose();};
  }, [context, request]);
  const container = request.container, targetId = container?.id ?? request.itemId;
  const [view, setView] = useState<DownloadOptions>(), [blocked, setBlocked] = useState(false), [policy, setPolicy] = useState<DownloadEpisodes>('all'), [keepNext, setKeepNext] = useState(1);
  const [busy, setBusy] = useState(''), [done, setDone] = useState(''), [error, setError] = useState<unknown>();
  const [acceptedSingle, setAcceptedSingle] = useState(false);
  useSyncExternalStore(opening?.current() ? opening.lease.device.subscribe : noSubscribe,
    () => opening?.current() ? opening.lease.device.getSnapshot() : undefined);
  useEffect(() => {
    if (!opening?.current()) return;
    void opening.lease.device.open().catch(failure => {if (opening.current()) setError(failure);});
  }, [opening]);
  // The episode question belongs to real shows and seasons; an album asks only for its files and a smaller copy.
  const episodic = container?.kind === 'show' || container?.kind === 'season';
  const selection = episodic ? policy : undefined, keep = episodic && policy === 'next' ? keepNext : undefined;
  useEffect(() => {
    if (!opening?.current() || !targetId) return;
    const abort = new AbortController(), linked = linkNativeAbortSignals([abort.signal, opening.signal]);
    setError(undefined); setDone(''); setAcceptedSingle(false); setView(undefined); setBlocked(false);
    // The whole-request size follows the chosen selection, so each change reads it again.
    void readDownloadOptions(opening.lease.api, targetId, {selection, keepNext: keep}, linked.signal)
      .then(next => {if (!linked.signal.aborted && opening.current()) setView(next);},
        failure => {if (linked.signal.aborted || !opening.current()) return; if (downloadsOff(failure)) setBlocked(true); else setError(failure);});
    return () => {abort.abort(); linked.dispose();};
  }, [opening, targetId, selection, keep]);
  const close = () => opening ? opening.close() : closeNativeDownload(request);
  const choose = async (quality: string) => {
    const ticket = opening?.claim();
    if (!opening || !ticket || !targetId) return;
    setBusy(quality); setError(undefined); setAcceptedSingle(false);
    try {
      // Capture this opening before any asynchronous device or server work.
      await opening.lease.device.open();
      if (!opening.owns(ticket)) return;
      const asked = await opening.lease.want({...(container?.kind === 'playlist' ? {listId: targetId} : {itemId: targetId}), quality,
        ...(selection ? {selection} : {}), ...(keep ? {keepNext: keep} : {})});
      if (!opening.owns(ticket)) return;
      setAcceptedSingle(!container);
      setDone(container ? [t('downloads.requestProgress', {ready: asked.counts.ready, total: asked.counts.titles}),
        ...(asked.counts.failed > 0 ? [t('downloads.requestFailed', {failed: asked.counts.failed})] : [])].join('; ') : t('downloads.willArrive'));
    } catch (failure) {
      if (opening.owns(ticket)) setError(failure);
    } finally {if (opening.owns(ticket)) setBusy(''); opening.release(ticket);}
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
  const titleOption = (option: DownloadChoice) => {
    const meaning = downloadSheetMeaning(option);
    const admission = option.available && opening?.current() ? opening.lease.device.admission?.(option.sizeBytes) : undefined;
    return qualityRow({quality: option.quality, label: option.quality === 'original' ? t('web.downloads.original') : option.label, available: option.available,
      size: option.available ? option.estimated ? t('downloads.aboutSize', {size: downloadBytes(option.sizeBytes)}) : downloadBytes(option.sizeBytes) : option.reason ?? '',
      notes: [admission || undefined, meaning ? t(meaning) : undefined, option.available && option.prepared ? t('downloads.serverPrepares') : undefined].filter((value): value is string => !!value),
      meta: busy === option.quality ? t('downloads.adding') : undefined});
  };
  const storage = view?.roomBytes;
  const paintCurrent = opening?.paintCurrent;
  const permit = paintCurrent ? overlayPaintPermit(paintCurrent, [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener)], request.lifetime ? [request.lifetime.signal] : []) : undefined;
  return <RetainedOverlay permit={permit} allowedTypes={[BottomSheet, React.Fragment, View, Text, Focusable, StateView]}><BottomSheet open onOpenChange={open => {if (!open) close();}} testID="native-download-sheet" title={t(done ? 'action.download' : 'downloads.chooseQuality')} description={request.title} fitContent
    footer={done ? <View style={{flexDirection: 'row', gap: 12}}><Focusable accessibilityRole="button" onPress={close} focusRadius={12} style={{minHeight: 48, paddingHorizontal: 16, alignItems: 'center', justifyContent: 'center'}}><Text style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{t('action.close')}</Text></Focusable>
      <Focusable accessibilityRole="button" onPress={() => {opening?.viewDownloads();}} focusRadius={12} style={{minHeight: 48, paddingHorizontal: 20, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.text, borderRadius: 12}}><Text style={[theme.type.bodyStrong, {color: theme.colors.ink}]}>{t('downloads.view')}</Text></Focusable></View> : undefined}>
    <View style={{gap: 16}}>
      {error !== undefined && <StateView {...{...downloadError(error), tone: 'error' as const}}/>}
      {done ? <>{acceptedSingle ? <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('downloads.addedMany', {count: 1})}</Text> : null}<Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{done}</Text></> : <>
        {blocked && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('downloads.offForProfile')}</Text>}
        {episodic && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{policyRow('all', t('downloads.policyAll'))}{policyRow('unwatched', t('downloads.policyUnwatched'))}{policyRow('next', t('downloads.policyNext'))}</View>}
        {episodic && policy === 'next' && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{[1, 3, 5].map(count => <Focusable key={count} accessibilityRole="button" accessibilityState={{selected: keepNext === count}} onPress={() => {if (opening?.current() && !busy) setKeepNext(count);}} focusRadius={12}
          style={{minHeight: 48, paddingHorizontal: 16, flexDirection: 'row', alignItems: 'center'}}><Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{t('downloads.keepNext', {count})}</Text>{keepNext === count && <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/>}</Focusable>)}</View>}
        {!done && !view && !blocked && error === undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('downloads.checking')}</Text>}
        {view && <View style={{backgroundColor: theme.colors.raised, borderRadius: 14}}>{view.items.map(titleOption)}</View>}
        {storage != null && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('downloads.spaceLeft', {size: downloadBytes(storage)})}</Text>}
      </>}
    </View>
  </BottomSheet></RetainedOverlay>;
}

/** A real read failure keeps its catalogue wording; an unknown failure is not
 * dressed as a download refusal. */
function downloadError(error: unknown) {
  return {title: defaultI18n.t('downloads.error.action'), body: error instanceof Error ? error.message : undefined};
}
