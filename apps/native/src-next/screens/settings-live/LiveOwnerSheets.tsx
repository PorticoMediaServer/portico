import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button, Segmented, Select, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {LoadingBoundary, SkeletonBlock, StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeLiveOwnerController, RemoteSourceDraft, RemoteSourcePreview, SourceKind, TunerDiscovery} from './live-owner-controller';
import {newRemoteDraft} from './live-owner-controller';
import {importSkipped} from '../../../../../packages/client-core/src/server-admin/live-words';

const kinds: readonly SourceKind[] = ['m3u', 'xtream', 'hdhomerun'];
const refreshOptions = ['3600000', '21600000', '43200000', '86400000'] as const;
const refreshKey = {'3600000': 'web.libraries.everyHour', '21600000': 'web.libraries.every6h', '43200000': 'web.liveTv.every12h', '86400000': 'web.libraries.daily'} as const;

/** Add a playlist or tuner the way the web does: check the source, show what it found, then save. */
export function AddSourceSheet({controller, runtime, open, onOpenChange, tuner, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; tuner?: string; onSaved: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [draft, setDraft] = useState<RemoteSourceDraft>(() => newRemoteDraft(controller.lifetime.authority.profileId));
  const [preview, setPreview] = useState<RemoteSourcePreview>(), [confirm, setConfirm] = useState<readonly string[]>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const cancel = useRef<View>(null);
  useEffect(() => {
    if (!open) return;
    setDraft({...newRemoteDraft(controller.lifetime.authority.profileId), ...(tuner ? {kind: 'hdhomerun' as SourceKind, locator: tuner, name: t('server.live.kind.hdhomerun'), useDiscoveredCapacity: true} : {})});
    setPreview(undefined); setConfirm(undefined); setError(undefined); setBusy(false);
  }, [open, tuner, controller, t]);
  const update = (patch: Partial<RemoteSourceDraft>) => {setDraft(current => ({...current, ...patch})); setPreview(undefined);};
  const ready = draft.name.trim().length > 0 && draft.locator.trim().length > 0;
  const check = async (next: RemoteSourceDraft) => {
    setBusy(true); setError(undefined);
    try {
      setPreview(await controller.previewRemote(next));
      setConfirm(undefined);
    } catch (failure) {
      const roots = (failure as {confirmRoots?: unknown}).confirmRoots;
      if (Array.isArray(roots) && roots.length && roots.every(value => typeof value === 'string')) setConfirm(roots as string[]);
      else setError(failure);
    } finally {setBusy(false);}
  };
  const save = async () => {
    if (!preview) return;
    setBusy(true); setError(undefined);
    const result = await controller.saveRemote(preview.previewId);
    setBusy(false);
    if (result) onSaved();
  };
  const skipped = preview ? importSkipped(t, id => defaultI18n.has(id), preview.preview.diagnostics) : undefined;
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button ref={cancel} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
    {confirm ? <Button variant="primary" loading={busy} onPress={() => {const next = {...draft, confirmedLanRoots: [...new Set([...draft.confirmedLanRoots, ...confirm])]}; setDraft(next); void check(next);}}>{t('web.liveTv.lanConfirm')}</Button>
      : preview ? <Button variant="primary" loading={busy} onPress={() => void save()}>{t('web.liveTv.addSource')}</Button>
        : <Button variant="primary" loading={busy} disabled={!ready} onPress={() => void check(draft)}>{t('web.liveTv.checkSource')}</Button>}
  </View>;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveTv.addDialog')} description={t('web.liveTv.addDialogLede')} initialFocusRef={cancel} footer={footer} testID="live-owner-add-source">
    <View style={{gap: 16}}>
      <Segmented testID="live-owner-add-kind" accessibilityLabel={t('web.libraries.kind')} value={draft.kind} items={kinds.map(kind => ({value: kind, label: t(`server.live.kind.${kind}` as Parameters<typeof t>[0])}))} onValueChange={value => update({kind: value as SourceKind})}/>
      <TextField testID="live-owner-add-name" label={t('web.liveTv.sourceName')} value={draft.name} maxLength={100} onChangeText={name => update({name})}/>
      <TextField testID="live-owner-add-locator" label={draft.kind === 'hdhomerun' ? t('web.liveTv.tunerAddress') : draft.kind === 'xtream' ? t('web.liveTv.serverAddress') : t('web.liveTv.playlistUrl')} value={draft.locator} autoCapitalize="none" autoCorrect={false} onChangeText={locator => update({locator})}/>
      <TextField testID="live-owner-add-guide" label={`${t('web.liveTv.guideUrl')} · ${t('field.optional')}`} description={t(draft.kind === 'hdhomerun' ? 'server.live.guideUrlTunerHelp' : 'server.live.guideUrlHelp')} value={draft.guideUrl} autoCapitalize="none" autoCorrect={false} onChangeText={guideUrl => update({guideUrl})}/>
      {draft.kind === 'xtream' && <>
        <TextField testID="live-owner-add-username" label={t('web.direct.username')} value={draft.username} autoCapitalize="none" autoCorrect={false} onChangeText={username => update({username})}/>
        <TextField testID="live-owner-add-password" label={t('web.account.password')} value={draft.password} secureTextEntry autoCapitalize="none" autoCorrect={false} onChangeText={password => update({password})}/>
      </>}
      <Select testID="live-owner-add-access" label={t('web.liveTv.whoCanWatch')} value={draft.viewerAccess} options={[{value: 'server-members', label: t('web.liveTv.everyoneOnServer')}, {value: 'owner-only', label: t('web.together.onlyMe')}]} onValueChange={viewerAccess => update({viewerAccess: viewerAccess as RemoteSourceDraft['viewerAccess']})}/>
      <Select testID="live-owner-add-refresh" label={t('web.liveTv.refreshGuide')} value={String(draft.refreshMs)} options={refreshOptions.map(value => ({value, label: t(refreshKey[value])}))} onValueChange={refresh => update({refreshMs: Number(refresh)})}/>
      {error !== undefined && <StateView testID="live-owner-add-error" {...runtime.error(error, () => setError(undefined))}/>}
      {preview && <View testID="live-owner-add-preview" style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: 14, gap: 6}}>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[t('server.live.channelCount', {count: preview.preview.channels}), t('server.live.programCount', {count: preview.preview.programmes}), ...(preview.discoveredCapacity ? [t('web.liveTv.capacityTuners', {count: preview.discoveredCapacity})] : [])].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(15, 700), {color: theme.colors.text}]}>{part}</Text>)}</View>
        <Text numberOfLines={3} style={[shellType(13), {color: theme.colors.textSecondary}]}>{preview.preview.names.slice(0, 8).join(', ')}{preview.preview.names.length > 8 ? '…' : ''}</Text>
        {skipped && <Text style={[shellType(13), {color: theme.colors.amber}]}>{[skipped.summary, ...skipped.reasons].join(' ')}</Text>}
      </View>}
      {confirm && <View testID="live-owner-add-lan" style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: 14, gap: 6}}>
        <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{t('web.liveTv.lanTitle')}</Text>
        <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.liveTv.lanBody')}</Text>
        {confirm.map(root => <Text key={root} style={[shellType(13), {color: theme.colors.textSecondary}]}>{root}</Text>)}
      </View>}
    </View>
  </Dialog>;
}

/** Looking for tuners never adds one by itself. */
export function DiscoverSheet({controller, runtime, open, onOpenChange, onUse}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onUse: (address: string) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [result, setResult] = useState<TunerDiscovery>(), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const search = async () => {
    setBusy(true); setError(undefined);
    try {setResult(await controller.discover());}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  useEffect(() => {if (open) {setResult(undefined); void search();}}, [open]); // eslint-disable-line react-hooks/exhaustive-deps
  const close = useRef<View>(null);
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.discoverTitle')} description={t('web.liveSource.discoverLede')} initialFocusRef={close}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button ref={close} variant="glass" onPress={() => onOpenChange(false)}>{t('action.close')}</Button><Button variant="glass" icon="i-refresh" loading={busy} onPress={() => void search()}>{t('web.liveSource.searchAgain')}</Button></View>} testID="live-owner-discover">
    <View style={{gap: 12}}>
      {error !== undefined && <StateView testID="live-owner-discover-error" {...runtime.error(error, () => void search())}/>}
      <LoadingBoundary ready={!busy || !!result} skeleton={<View style={{gap: 10}}><SkeletonBlock height={64} radius={12}/><SkeletonBlock height={64} radius={12}/></View>}>
        <>
      {result && !result.discoveryEnabled && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.liveSource.searchOff')}</Text>}
      {result && result.discoveryEnabled && !result.devices.length && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{result.message || t('web.liveSource.noTuners')}</Text>}
      {result?.devices.map(found => <View key={found.deviceId || found.address} style={{flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: 14}}>
        <View style={{flex: 1, gap: 4}}>
          <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{found.friendlyName || found.model || t('web.liveSource.tunerFallback')}</Text>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[found.model, found.address, found.tunerCount ? t('server.live.tunerCount', {count: found.tunerCount}) : ''].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(13), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View>
        </View>
        {found.configured ? <Text style={[shellType(13, 700), {color: theme.colors.textSecondary}]}>{t('web.liveSource.alreadyAdded')}</Text>
          : <Button variant="glass" size="small" onPress={() => {onUse(found.baseUrl || found.address); onOpenChange(false);}}>{t('web.liveSource.useThis')}</Button>}
      </View>)}
        </>
      </LoadingBoundary>
    </View>
  </Dialog>;
}
