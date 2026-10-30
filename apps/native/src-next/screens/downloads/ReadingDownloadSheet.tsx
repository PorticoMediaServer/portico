import React, {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {downloadBytes, downloadReasonMessage, parseDownloadBatch, parseDownloadOptions, type DownloadOptionsView} from '../../../../../packages/client-core/src/downloads';
import type {ReadingClient} from '../../../../../packages/client-core/src/reading';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {Button, Focusable} from '../../kit/controls';
import {BottomSheet, Popover, SidePanel} from '../../kit/overlays';
import {overlayPaintKey, overlayPaintPermit, RetainedOverlay} from '../../kit/overlays/SurfacePaintHost';
import {StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';
import {alpha, browseType} from '../../kit/browse/presentation';
import {createNativeDownloadsDevice} from './device';
import {getNativeDownloadSnapshot, closeNativeDownload, type NativeDownloadRequest} from './request';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {captureNativeDownloadPaintCurrent} from './download-opening';

type Version = Readonly<{sourceId: string; format: string; size: number; inventoryAvailable: boolean; policy: DownloadOptionsView}>;
type Opening = {current: () => boolean; paintCurrent: () => boolean; retire: () => void; dispose: () => void; pending: boolean; lease: ReturnType<typeof createNativeDownloadsDevice>; versions: readonly Version[]; abort: AbortController; signal: AbortSignal};
type Family = {retired: boolean; active?: Opening;
  /** Private: the exact context and request this family was constructed for. Reading them here
   * keeps the memo's original keys honest without publishing or comparing them anywhere else. */
  binding: Readonly<{context: NativeShellBootstrapContext; request: NativeDownloadRequest}>};
type State = Readonly<{opening?: Opening; versions?: readonly Version[]; policy?: DownloadOptionsView; busy?: string; error?: unknown}>;

/** The existing download sheet's original-publication branch. Real source IDs
 * and exact byte sizes come from Reading versions; no media-quality ladder. */
export function NativeReadingDownloadSheet({context, request}: {context: NativeShellBootstrapContext; request: NativeDownloadRequest}) {
  const theme = useTheme(), t = defaultI18n.t;
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
    const opening: Opening = {current, paintCurrent, pending: false, lease, versions: Object.freeze([]), abort, signal,
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
      const supported = versions.versions.filter(value => value.format === 'epub' || value.format === 'pdf' || value.format === 'cbz');
      const rows: Version[] = []; let next = 0;
      await Promise.all(Array.from({length: Math.min(4, supported.length)}, async () => {
        while (next < supported.length) {
          const index = next++, version = supported[index]; if (!version || !current()) return;
          const raw = await context.api.request<unknown>(`/v1/items/${encodeURIComponent(itemId)}/download-options?versionId=${encodeURIComponent(version.sourceId)}`, 'GET', undefined, signal);
          if (!current()) return;
          const policy = parseDownloadOptions(raw, itemId);
          if (policy.source.bytes !== version.size || policy.source.available && policy.source.container !== version.format) throw new Error('reading_source_changed');
          rows[index] = Object.freeze({...version, policy});
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
    if (!opening.current() || opening.pending || !opening.versions.includes(version) || !version.inventoryAvailable || !['epub', 'pdf', 'cbz'].includes(version.format) || !policy.policy.allowDownloads || !policy.source.available || !policy.options.some(value => value.quality === 'original' && value.available) || !Number.isSafeInteger(version.size) || version.size < 1 || version.size > 2 ** 31) return;
    const device = opening.lease.device.getSnapshot();
    if (!device.ready || opening.lease.device.admission?.(version.size) || version.size > device.freeBytes || policy.storage.remainingBytes !== null && policy.storage.remainingBytes !== undefined && version.size > policy.storage.remainingBytes) return;
    opening.pending = true; setState(value => value.opening === opening && opening.current() ? {...value, busy: version.sourceId, error: undefined} : value);
    try {
      const operationId = 'reading_' + Array.from(nativeRouteCrypto.random(16), byte => byte.toString(16).padStart(2, '0')).join('');
      if (!opening.current()) return;
      const raw = await context.api.request<unknown>('/v1/downloads/preparations', 'POST', {operationId, mediaId: request.itemId, versionId: version.sourceId, quality: 'original'}, opening.signal);
      if (!opening.current()) return;
      const result = parseDownloadBatch(raw);
      const preparation = result.items.find(value => value.itemId === request.itemId && value.versionId === version.sourceId && value.quality === 'original');
      if (!preparation) throw new Error(result.rejected[0]?.code ?? 'reading_unavailable');
      await opening.lease.wantPublication(preparation, opening.lease.service);
      if (opening.current()) close(opening);
    } catch (error) {if (opening.current()) setState(value => value.opening === opening && opening.current() ? {...value, error} : value);}
    finally {opening.pending = false; if (opening.current()) setState(value => value.opening === opening && opening.current() ? {...value, busy: undefined} : value);}
  };
  const shown: State = state.opening?.current() ? state : {};
  const blocked = shown.policy && !shown.policy.policy.allowDownloads;
  // tablet-read12b: the same versions and real preparation, chosen before the one
  // primary action. A side panel keeps the book visible on handheld tablets; the
  // phone keeps its sheet. No recommended flag is published, so the first version
  // the server lists as available is selected rather than claiming a recommendation.
  const versions = shown.versions?.filter(value => value.format === 'epub' || value.format === 'pdf' || value.format === 'cbz') ?? [];
  const opened = shown.opening;
  const ready = (version: Version) => {
    // A retained event must retire with its exact opening before touching the
    // borrowed device; a stale opening's lease is already released.
    if (!opened || !opened.current()) return false;
    const policy = version.policy, device = opened.lease.device.getSnapshot();
    return device.ready && !!policy && version.inventoryAvailable && policy.policy.allowDownloads && policy.source.available
      && policy.options.some(value => value.quality === 'original' && value.available) && Number.isSafeInteger(version.size) && version.size > 0 && version.size <= 2 ** 31 && !shown.busy
      && !opened.lease.device.admission?.(version.size) && version.size <= device.freeBytes
      && (policy.storage.remainingBytes === undefined || policy.storage.remainingBytes === null || version.size <= policy.storage.remainingBytes);
  };
  // An explicit honest choice stays chosen even if it becomes unavailable; the
  // primary action then disables instead of silently switching the format.
  const selectedVersion = versions.find(value => value.sourceId === selected) ?? versions.find(ready) ?? versions[0];
  const opening = shown.opening, freeBytes = opening?.lease.device.getSnapshot().freeBytes;
  const admission = selectedVersion && opening?.current() ? opening.lease.device.admission?.(selectedVersion.size) : undefined;
  const surface = {open: true as const, title: t('action.download'), description: request.title, testID: 'native-reading-download-sheet',
    onOpenChange: (open: boolean) => {if (!open) close(shown.opening);}};
  // tablet-read#1b/12b: the upright side panel draws its own radio rows and a
  // stacked action group, while the phone sheet and the anchored popover keep the
  // exact presentation they have today. Geometry and typography here are caller
  // props; no shared kit, type or guard changes.
  const primaryAction = (side: boolean) => selectedVersion ? <Focusable testID="reading-download-primary" accessibilityRole="button" disabled={!ready(selectedVersion) || !!shown.busy}
      onPress={() => {if (opened && ready(selectedVersion)) void choose(opened, selectedVersion);}}
      style={side
        ? {height: 54, borderRadius: 12, paddingHorizontal: 26, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 10,
            backgroundColor: ready(selectedVersion) && !shown.busy ? theme.colors.text : theme.colors.raisedBright, opacity: ready(selectedVersion) && !shown.busy ? 1 : .55}
        : {minHeight: 50, borderRadius: 12, paddingHorizontal: 18, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 8,
            backgroundColor: ready(selectedVersion) && !shown.busy ? theme.colors.text : theme.colors.raisedBright, opacity: ready(selectedVersion) && !shown.busy ? 1 : .55}}>
      {side ? <Icon name="i-dl" size={22} color={theme.colors.ink}/> : null}
      <Text style={side ? [browseType(theme, 17, 700), {color: theme.colors.ink}] : [theme.type.bodyStrong, {color: theme.colors.ink}]}>{t(shown.busy ? 'downloads.adding' : 'action.download')}</Text>
      <Text style={side ? [browseType(theme, 17, 700), {color: theme.colors.ink}] : [theme.type.caption, {color: theme.colors.ink}]}>{downloadBytes(selectedVersion.size)}</Text>
    </Focusable> : null;
  const status = <>
      {!shown.versions && shown.error === undefined && nativePublicationReadsAvailable() && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text>}
      {(!nativePublicationReadsAvailable() || shown.versions && !versions.length) && <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}/>}
      {blocked && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{downloadReasonMessage(shown.policy!.policy.reason) || t('downloads.offForProfile')}</Text>}
      {!blocked && !!admission && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{admission}</Text>}
      {shown.error !== undefined && <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
        primaryAction={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (shown.opening?.current() && !shown.opening.pending) setRetry(value => value + 1);}}}/>}
    </>;
  const renderBody = (side: boolean) => <View style={side ? {flex: 1} : {gap: 12}}>
      {side ? <View style={{gap: 12}}>{status}</View> : status}
      {versions.map(version => {
        const available = ready(version), selectedVersionId = selectedVersion?.sourceId === version.sourceId;
        return <Focusable key={version.sourceId} testID={`reading-download-${version.sourceId}`} accessibilityRole={side ? 'radio' : 'button'} disabled={!available}
          focusKind="row" focusRadius={side ? 14 : 12} accessibilityState={side ? {checked: selectedVersionId, disabled: !available} : {selected: selectedVersionId}}
          onPress={() => {if (available) setSelected(version.sourceId);}}
          style={side
            ? {minHeight: 78, marginBottom: 10, borderRadius: 14, padding: 16, flexDirection: 'row', alignItems: 'center', gap: 14, opacity: available ? 1 : .5,
                backgroundColor: selectedVersionId ? alpha(theme.colors.accent, .16) : theme.colors.raisedHigh}
            : {minHeight: 64, borderRadius: 14, paddingHorizontal: 14, paddingVertical: 12, flexDirection: 'row', alignItems: 'center', gap: 12, opacity: available ? 1 : .5,
                backgroundColor: theme.colors.raisedHigh, borderWidth: selectedVersionId ? 1.5 : 0, borderColor: alpha(theme.colors.accentStrong, .75)}}>
          {side && selectedVersionId ? <View pointerEvents="none" accessible={false}
            style={{position: 'absolute', left: 0, right: 0, top: 0, bottom: 0, borderRadius: 14, borderWidth: 2, borderColor: alpha(theme.colors.accentStrong, .7)}}/> : null}
          {side ? <View style={{width: 24, height: 24, borderRadius: 12, alignItems: 'center', justifyContent: 'center', borderWidth: 2,
              borderColor: selectedVersionId ? theme.colors.accentStrong : theme.colors.lineStrong}}>
            {selectedVersionId ? <View style={{width: 10, height: 10, borderRadius: 5, backgroundColor: theme.colors.accentStrong}}/> : null}
          </View> : null}
          {side
            ? <><Text style={[browseType(theme, 16, 800), {color: theme.colors.text, flex: 1}]}>{version.format.toUpperCase()}</Text>
                <Text style={[browseType(theme, 14.5, 700), {color: theme.colors.textSecondary}]}>{downloadBytes(version.size)}</Text></>
            : <><View style={{width: 22, height: 22, borderRadius: 11, alignItems: 'center', justifyContent: 'center', borderWidth: 2,
                borderColor: selectedVersionId ? theme.colors.accentStrong : theme.colors.lineStrong}}>
                {selectedVersionId ? <View style={{width: 10, height: 10, borderRadius: 5, backgroundColor: theme.colors.accentStrong}}/> : null}
              </View>
                <Text style={[browseType(theme, 15, 700), {color: theme.colors.text, flex: 1}]}>{version.format.toUpperCase()}</Text>
                <Text style={[browseType(theme, 13, 700), {color: theme.colors.textSecondary}]}>{downloadBytes(version.size)}</Text></>}
        </Focusable>;
      })}
      {freeBytes !== undefined && <Text style={side ? [browseType(theme, 13.5, 400), {color: theme.colors.textTertiary, marginTop: 6}] : [theme.type.caption, {color: theme.colors.textTertiary}]}>{t(theme.platform === 'phone' ? 'next.saved.phone.free' : 'downloads.freeOnDevice', {size: downloadBytes(freeBytes)})}</Text>}
      {side
        ? <View style={{marginTop: 'auto', gap: 10}}>
            {primaryAction(true)}
            <Button variant="glass" testID="reading-download-cancel" onPress={() => surface.onOpenChange(false)}
              style={{height: 54, borderRadius: 12, paddingHorizontal: 26, alignSelf: 'stretch'}} textStyle={[browseType(theme, 17, 700)]}>{t('action.cancel')}</Button>
          </View>
        : primaryAction(false)}
    </View>;
  // tablet-read12b: on the expanded width the same real choice opens under the
  // book's own Download button. The original page lifetime is already linked, and a
  // replaced route or viewer cannot take the popover over.
  // An upright tablet uses the platform's side panel (frame 12b), the expanded width keeps
  // the popover under the book's own button, and the phone keeps its sheet.
  const anchored = !!request.anchorRef && theme.width >= 1024;
  const paintCurrent = opened?.paintCurrent;
  const permit = paintCurrent ? overlayPaintPermit(paintCurrent,
    [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener)], request.lifetime ? [request.lifetime.signal] : []) : undefined;
  const node = anchored
    ? <Popover {...surface} anchorRef={request.anchorRef} placement="bottom-end" offset={8} width={360} showTitle>{renderBody(false)}</Popover>
    : theme.platform === 'phone'
      ? <BottomSheet {...surface} fitContent>{renderBody(false)}</BottomSheet>
      : <SidePanel {...surface} width={360} topInset={0} style={{paddingTop: 66, paddingBottom: 28}} contentStyle={{paddingHorizontal: 24}}
          titleType={{size: 26, lineHeight: 'normal', weight: 800, tracking: -.03}} descriptionTextStyle={{fontSize: 15, lineHeight: 22.5}}>{renderBody(true)}</SidePanel>;
  return <RetainedOverlay key={overlayPaintKey(request)} permit={permit}
    allowedTypes={[BottomSheet, Popover, SidePanel, React.Fragment, View, Text, Focusable, Button, StateView, Icon]}>{node}</RetainedOverlay>;
}
