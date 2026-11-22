import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Button, Segmented, Select, Switch, TextField} from '../controls';
import {Dialog} from '../overlays';
import {ChannelMark} from '../artwork';
import {Icon} from '../Icon';
import {LoadingBoundary, SkeletonBlock, StateView} from '../states';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController, RemoteSourceDraft, SourceKind} from '../../screens/settings-live/live-owner-controller';
import type {LiveSource} from '../../screens/settings-live/live-owner-model';
import {refreshKey, refreshOptions, sourceKinds, useAddSource, useChannelMap, useDiscover} from '../../pages/shared/live/useLiveSourceSheets';
import {useUploadSource} from '../../pages/shared/live/useUploadSource';

/** A raised card inside a dialog: what a check found, the home-network question, a tuner found nearby. */
function FoundCard({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{backgroundColor: theme.colors.raisedHigh, borderRadius: 12, padding: theme.space.sm - 2, gap: theme.space.tight}}>{children}</View>;
}
function Facts({parts, strong = false}: {parts: readonly string[]; strong?: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap, rowGap: 3}}>
    {parts.map((part, index) => <Text key={index} style={[strong ? theme.type.rowLabel : theme.type.rowHelp, {color: strong ? theme.colors.text : theme.colors.textTertiary}]}>{part}</Text>)}
  </View>;
}
function Line({children, tone = 'secondary'}: {children: ReactNode; tone?: 'text' | 'secondary' | 'tertiary' | 'amber'}) {
  const theme = useTheme();
  const color = {text: theme.colors.text, secondary: theme.colors.textSecondary, tertiary: theme.colors.textTertiary, amber: theme.colors.amber}[tone];
  return <Text style={[tone === 'text' ? theme.type.rowLabel : theme.type.rowHelp, {color}]}>{children}</Text>;
}
function Column({children, gap = 'sm'}: {children: ReactNode; gap?: 'sm' | 'md'}) {
  const theme = useTheme();
  return <View style={{gap: gap === 'md' ? theme.space.sm : theme.space.controlGap}}>{children}</View>;
}
function FootRow({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{children}</View>;
}

/** Add source: kind, name, address, guide, sign-in for Xtream, who can watch and how often; Check source shows what it
 * found before Add source saves it. */
export function AddSourceSheet({controller, runtime, open, onOpenChange, tuner, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; tuner?: string; onSaved: () => void}) {
  const view = useAddSource({controller, runtime, open, tuner, onSaved}), {t, draft, busy, found, confirm} = view;
  const footer = <FootRow>
    <Button ref={view.cancel} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
    {view.step === 'confirm' ? <Button variant="primary" loading={busy} onPress={view.confirmLan}>{t('web.liveTv.lanConfirm')}</Button>
      : view.step === 'save' ? <Button variant="primary" loading={busy} onPress={view.save}>{t('web.liveTv.addSource')}</Button>
        : <Button variant="primary" loading={busy} disabled={!view.ready} onPress={view.check}>{t('web.liveTv.checkSource')}</Button>}
  </FootRow>;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveTv.addDialog')} description={t('web.liveTv.addDialogLede')} initialFocusRef={view.cancel} footer={footer} testID="live-owner-add-source">
    <Column gap="md">
      <Segmented testID="live-owner-add-kind" accessibilityLabel={t('web.libraries.kind')} value={draft.kind} items={sourceKinds.map(kind => ({value: kind, label: t(`server.live.kind.${kind}` as Parameters<typeof t>[0])}))} onValueChange={value => view.update({kind: value as SourceKind})}/>
      <TextField testID="live-owner-add-name" label={t('web.liveTv.sourceName')} value={draft.name} maxLength={100} onChangeText={name => view.update({name})}/>
      <TextField testID="live-owner-add-locator" label={view.locatorLabel} value={draft.locator} autoCapitalize="none" autoCorrect={false} onChangeText={locator => view.update({locator})}/>
      <TextField testID="live-owner-add-guide" label={`${t('web.liveTv.guideUrl')} · ${t('field.optional')}`} description={t(draft.kind === 'hdhomerun' ? 'server.live.guideUrlTunerHelp' : 'server.live.guideUrlHelp')} value={draft.guideUrl} autoCapitalize="none" autoCorrect={false} onChangeText={guideUrl => view.update({guideUrl})}/>
      {draft.kind === 'xtream' && <>
        <TextField testID="live-owner-add-username" label={t('web.direct.username')} value={draft.username} autoCapitalize="none" autoCorrect={false} onChangeText={username => view.update({username})}/>
        <TextField testID="live-owner-add-password" label={t('web.account.password')} value={draft.password} secureTextEntry autoCapitalize="none" autoCorrect={false} onChangeText={password => view.update({password})}/>
      </>}
      <Select testID="live-owner-add-access" label={t('web.liveTv.whoCanWatch')} value={draft.viewerAccess} options={[{value: 'server-members', label: t('web.liveTv.everyoneOnServer')}, {value: 'owner-only', label: t('web.together.onlyMe')}]} onValueChange={viewerAccess => view.update({viewerAccess: viewerAccess as RemoteSourceDraft['viewerAccess']})}/>
      <Select testID="live-owner-add-refresh" label={t('web.liveTv.refreshGuide')} value={String(draft.refreshMs)} options={refreshOptions.map(value => ({value, label: t(refreshKey[value])}))} onValueChange={refresh => view.update({refreshMs: Number(refresh)})}/>
      {view.error !== undefined && <StateView testID="live-owner-add-error" {...runtime.error(view.error, view.clearError)}/>}
      {found && <FoundCard testID="live-owner-add-preview">
        <Facts parts={found.facts} strong/>
        <Line>{found.names}</Line>
        {found.skipped !== undefined && <Line tone="amber">{found.skipped}</Line>}
      </FoundCard>}
      {confirm && <FoundCard testID="live-owner-add-lan">
        <Line tone="text">{t('web.liveTv.lanTitle')}</Line>
        <Line>{t('web.liveTv.lanBody')}</Line>
        {confirm.map(root => <Line key={root}>{root}</Line>)}
      </FoundCard>}
    </Column>
  </Dialog>;
}

/** Discover: tuners on the network, each with Use this (or Already added); Search again. */
export function DiscoverSheet({controller, runtime, open, onOpenChange, onUse}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onUse: (address: string) => void}) {
  const t = runtime.text.t, theme = useTheme(), view = useDiscover({controller, open}), {result} = view;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.discoverTitle')} description={t('web.liveSource.discoverLede')} initialFocusRef={view.close}
    footer={<FootRow><Button ref={view.close} variant="glass" onPress={() => onOpenChange(false)}>{t('action.close')}</Button><Button variant="glass" icon="i-refresh" loading={view.busy} onPress={view.search}>{t('web.liveSource.searchAgain')}</Button></FootRow>} testID="live-owner-discover">
    <Column>
      {view.error !== undefined && <StateView testID="live-owner-discover-error" {...runtime.error(view.error, view.search)}/>}
      <LoadingBoundary ready={!view.busy || !!result} skeleton={<Column><SkeletonBlock height={64} radius={12}/><SkeletonBlock height={64} radius={12}/></Column>}>
        <>
          {result && !result.discoveryEnabled && <Line>{t('web.liveSource.searchOff')}</Line>}
          {result && result.discoveryEnabled && !result.devices.length && <Line>{result.message || t('web.liveSource.noTuners')}</Line>}
          {result?.devices.map(found => <FoundCard key={found.deviceId || found.address}>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4}}>
              <View style={{flex: 1, gap: 4}}>
                <Line tone="text">{found.friendlyName || found.model || t('web.liveSource.tunerFallback')}</Line>
                <Facts parts={[found.model, found.address, found.tunerCount ? t('server.live.tunerCount', {count: found.tunerCount}) : ''].filter(Boolean)}/>
              </View>
              {found.configured ? <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('web.liveSource.alreadyAdded')}</Text>
                : <Button variant="glass" size="small" onPress={() => {onUse(found.baseUrl || found.address); onOpenChange(false);}}>{t('web.liveSource.useThis')}</Button>}
            </View>
          </FoundCard>)}
        </>
      </LoadingBoundary>
    </Column>
  </Dialog>;
}

/** Channels and numbers: find a channel, hide it, or give it a number and a guide id; Save sends only the touched ones. */
export function ChannelMapSheet({controller, runtime, source, open, onOpenChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; open: boolean; onOpenChange: (open: boolean) => void}) {
  const t = runtime.text.t, theme = useTheme(), view = useChannelMap({controller, source, open}), {page} = view;
  const footer = <FootRow>
    <Button ref={view.close} variant="glass" disabled={view.busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
    <Button variant="primary" loading={view.busy} disabled={!view.changed} onPress={view.save}>{view.changed ? t('web.liveSource.saveMapChanges', {changed: view.changed}) : t('action.save')}</Button>
  </FootRow>;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.channelMapTitle', {name: source?.name ?? ''})} description={t('web.liveSource.channelMapLede')} initialFocusRef={view.close} footer={footer} testID="live-owner-channel-map">
    <Column>
      {view.error !== undefined && (view.partial
        ? <Text testID="live-owner-channel-map-partial" style={[theme.type.rowHelp, {color: theme.colors.amber}]}>{t('web.liveSource.channelMapPartial', view.partial)}</Text>
        : <StateView testID="live-owner-channel-map-error" {...runtime.error(view.error, view.retry, 'live')}/>)}
      <TextField testID="live-owner-channel-search" variant="search" leadingIcon="i-search" accessibilityLabel={t('web.live.findChannel')} placeholder={t('web.live.findChannel')} value={view.filter} onChangeText={view.setFilter}/>
      <ScrollView style={{maxHeight: 420}} contentContainerStyle={{gap: theme.space.controlGap}} keyboardShouldPersistTaps="handled">
        <LoadingBoundary ready={!view.loading || !!page} immediate={!view.loading || !!page} skeleton={<Column><SkeletonBlock height={84} radius={12}/><SkeletonBlock height={84} radius={12}/></Column>}>
          <>
            {view.rows.map(({entry, value, touched, edit}) => <View key={entry.channelId} testID={`live-owner-channel-${entry.channelId}`}
              style={{gap: theme.space.xs, padding: theme.space.sm - 4, borderRadius: 12, backgroundColor: theme.colors.raisedHigh, borderWidth: touched ? 1.5 : 0, borderColor: theme.colors.accent, opacity: value.hidden ? .6 : 1}}>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}>
                <View style={{width: 28, height: 28, borderRadius: 6, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}>
                  {entry.mark ? <ChannelMark mark={{sourceId: page?.sourceId ?? '', id: entry.channelId, ...entry.mark}} size={28}/> : <Icon name="i-tv" size={18} color={theme.colors.textSecondary}/>}
                </View>
                <View style={{flex: 1}}>
                  <Line tone="text">{entry.name}</Line>
                  <Facts parts={[`${t('server.live.map.fromSource')} ${entry.sourceNumber || '—'}`, entry.group].filter(Boolean)}/>
                </View>
                <Switch testID={`live-owner-channel-hidden-${entry.channelId}`} accessibilityLabel={t('web.liveSource.hideChannel', {name: entry.name})} checked={value.hidden} onCheckedChange={hidden => edit({hidden})}/>
              </View>
              <View style={{flexDirection: 'row', gap: theme.space.xs}}>
                <TextField testID={`live-owner-channel-number-${entry.channelId}`} containerStyle={{flex: 1}} label={t('server.live.map.number')} value={value.number} maxLength={12} onChangeText={number => edit({number})}/>
                <TextField testID={`live-owner-channel-guide-${entry.channelId}`} containerStyle={{flex: 2}} label={t('server.live.map.guideId')} placeholder={t('player.quality.automatic')} value={value.guideChannelId} maxLength={200} autoCapitalize="none" autoCorrect={false} onChangeText={guideChannelId => edit({guideChannelId})}/>
              </View>
            </View>)}
            {!view.loading && page && !view.rows.length && <Line tone="tertiary">{t('web.liveSource.noChannels')}</Line>}
          </>
        </LoadingBoundary>
      </ScrollView>
      {view.more ? <Button variant="glass" size="small" loading={view.loading} onPress={view.more}>{t('action.showMoreChannels')}</Button> : null}
    </Column>
  </Dialog>;
}

/** Upload playlist files: a name, the M3U and an optional XMLTV guide, tuners; Check files shows what they hold before
 * Add source saves them. */
export function UploadSourceSheet({controller, runtime, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), view = useUploadSource({controller, runtime, open, onOpenChange, onSaved}), {t, busy, reading, found} = view;
  const fileRow = (field: 'playlist' | 'guide', label: 'web.liveTv.playlistFile' | 'web.liveTv.guideFile') => {
    const file = view.file(field);
    return <View style={{gap: theme.space.tight}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t(label)}</Text>
      <Button testID={`live-owner-upload-${field}`} variant="glass" icon="i-upload" loading={reading === field} disabled={busy || !!reading} onPress={file.choose}>{file.title ?? t('web.metadata.chooseImageFile')}</Button>
      {file.size !== undefined && <Line tone="tertiary">{file.size}</Line>}
    </View>;
  };
  return <Dialog open={view.shown} onOpenChange={view.dismiss} title={t('web.liveTv.upload')} description={t('web.liveTv.uploadLede')} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showClose={!busy}
    footer={<FootRow>
      <Button variant="glass" disabled={busy} onPress={view.close}>{t('action.cancel')}</Button>
      <Button variant="primary" loading={busy} disabled={!view.ready} onPress={view.primary}>{t(view.checked ? 'web.liveTv.addSource' : 'web.liveTv.checkFiles')}</Button>
    </FootRow>} testID="live-owner-upload">
    <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{gap: theme.space.sm}}>
      <TextField testID="live-owner-upload-name" label={t('web.liveTv.sourceName')} value={view.name} maxLength={100} editable={!(busy || !!reading)} onChangeText={view.setName}/>
      {fileRow('playlist', 'web.liveTv.playlistFile')}
      {fileRow('guide', 'web.liveTv.guideFile')}
      <TextField testID="live-owner-upload-tuners" label={t('web.dvrSettings.tuners')} description={t('web.liveTv.tunersHelp')} keyboardType="number-pad" value={view.tunerCount} editable={!(busy || !!reading)} onChangeText={view.setTuners}/>
      {view.error !== undefined && <StateView testID="live-owner-upload-error" {...runtime.error(view.error, view.clearError)}/>}
      {found && <FoundCard testID="live-owner-upload-preview">
        <Facts parts={found.facts} strong/>
        {found.names !== undefined && <Line>{found.names}</Line>}
      </FoundCard>}
    </ScrollView>
  </Dialog>;
}
