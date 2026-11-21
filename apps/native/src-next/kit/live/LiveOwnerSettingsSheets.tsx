import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Button, Switch, TextField} from '../controls';
import {Dialog} from '../overlays';
import {ChannelMark} from '../artwork';
import {Icon} from '../Icon';
import {LoadingBoundary, SkeletonBlock, StateView} from '../states';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import type {ChannelMapEdit, ChannelMapPage, LiveSource} from '../../screens/settings-live/live-owner-model';
import {useLiveChannelMap} from '../../pages/shared/settings/useLiveChannelMap';



export function ChannelMapSheet({controller, runtime, source, open, onOpenChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; open: boolean; onOpenChange: (open: boolean) => void}) {
  const theme = useTheme();
  const {t, close, error, changed, busy, load, filter, setFilter, loading, page, shown, value, edits, edit, save} = useLiveChannelMap({controller, runtime, source, open, onOpenChange});
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button ref={close} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
    <Button variant="primary" loading={busy} disabled={!changed} onPress={() => void save()}>{changed ? t('web.liveSource.saveMapChanges', {changed}) : t('action.save')}</Button>
  </View>;
  const partial = error as {saved?: number; total?: number} | undefined;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.channelMapTitle', {name: source?.name ?? ''})} description={t('web.liveSource.channelMapLede')} initialFocusRef={close} footer={footer} testID="live-owner-channel-map">
    <View style={{gap: 12}}>
      {error !== undefined && (partial?.saved !== undefined
        ? <Text testID="live-owner-channel-map-partial" style={[theme.type.rowHelp, {color: theme.colors.amber}]}>{t('web.liveSource.channelMapPartial', {saved: partial.saved ?? 0, total: partial.total ?? changed})}</Text>
        : <StateView testID="live-owner-channel-map-error" {...runtime.error(error, () => {if (source) void load(source.id);}, 'live')}/>)}
      <TextField testID="live-owner-channel-search" variant="search" leadingIcon="i-search" accessibilityLabel={t('web.live.findChannel')} placeholder={t('web.live.findChannel')} value={filter} onChangeText={setFilter}/>
      <ScrollView style={{maxHeight: 420}} contentContainerStyle={{gap: 10}} keyboardShouldPersistTaps="handled">
        <LoadingBoundary ready={!loading || !!page} immediate={!loading || !!page} skeleton={<View style={{gap: 8}}><SkeletonBlock height={84} radius={12}/><SkeletonBlock height={84} radius={12}/></View>}>
        <>
        {shown.map(entry => {
          const current = value(entry.channelId, entry), touched = edits[entry.channelId] !== undefined;
          return <View key={entry.channelId} testID={`live-owner-channel-${entry.channelId}`} style={{gap: 8, padding: 12, borderRadius: 12, backgroundColor: theme.colors.raisedHigh, borderWidth: touched ? 1.5 : 0, borderColor: theme.colors.accent, opacity: current.hidden ? .6 : 1}}>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
              <View style={{width: 28, height: 28, borderRadius: 6, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}>
                {entry.mark ? <ChannelMark mark={{sourceId: page?.sourceId ?? '', id: entry.channelId, ...entry.mark}} size={28}/> : <Icon name="i-tv" size={18} color={theme.colors.textSecondary}/>}
              </View>
              <View style={{flex: 1}}>
                <Text style={[theme.type.rowLabel, {color: theme.colors.text}]} numberOfLines={1}>{entry.name}</Text>
                <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[`${t('server.live.map.fromSource')} ${entry.sourceNumber || '—'}`, entry.group].filter(Boolean).map((part, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]} numberOfLines={1}>{part}</Text>)}</View>
              </View>
              <Switch testID={`live-owner-channel-hidden-${entry.channelId}`} accessibilityLabel={t('web.liveSource.hideChannel', {name: entry.name})} checked={current.hidden} onCheckedChange={hidden => edit(entry, {hidden})}/>
            </View>
            <View style={{flexDirection: 'row', gap: 8}}>
              <TextField testID={`live-owner-channel-number-${entry.channelId}`} containerStyle={{flex: 1}} label={t('server.live.map.number')} value={current.number} maxLength={12} onChangeText={number => edit(entry, {number})}/>
              <TextField testID={`live-owner-channel-guide-${entry.channelId}`} containerStyle={{flex: 2}} label={t('server.live.map.guideId')} placeholder={t('player.quality.automatic')} value={current.guideChannelId} maxLength={200} autoCapitalize="none" autoCorrect={false} onChangeText={guideChannelId => edit(entry, {guideChannelId})}/>
            </View>
          </View>;
        })}
        {!loading && page && !shown.length && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('web.liveSource.noChannels')}</Text>}
        </>
        </LoadingBoundary>
      </ScrollView>
      {page?.nextCursor ? <Button variant="glass" size="small" loading={loading} onPress={() => {if (source && page) void load(source.id, page.nextCursor);}}>{t('action.showMoreChannels')}</Button> : null}
    </View>
  </Dialog>;
}
