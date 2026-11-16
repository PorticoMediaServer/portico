import React, {useEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Button, Switch, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {ChannelMark} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {LoadingBoundary, SkeletonBlock, StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from './live-owner-controller';
import type {ChannelMapEdit, ChannelMapPage, LiveSource} from './live-owner-model';

export function ChannelMapSheet({controller, runtime, source, open, onOpenChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; open: boolean; onOpenChange: (open: boolean) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [page, setPage] = useState<ChannelMapPage>(), [edits, setEdits] = useState<Record<string, ChannelMapEdit>>({}), [filter, setFilter] = useState(''), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false);
  const close = useRef<View>(null);
  const load = async (id: string, cursor = '') => {
    setLoading(true); setError(undefined);
    try {
      const next = await controller.channelMap(id, cursor);
      setPage(current => cursor && current ? {...next, items: [...current.items, ...next.items]} : next);
    } catch (failure) {setError(failure);}
    finally {setLoading(false);}
  };
  useEffect(() => {
    if (!open || !source) return;
    setPage(undefined); setEdits({}); setFilter(''); setError(undefined);
    void load(source.id);
  }, [open, source]); // eslint-disable-line react-hooks/exhaustive-deps
  const value = (channelId: string, entry: ChannelMapPage['items'][number]): ChannelMapEdit => edits[channelId] ?? {number: entry.overridden ? entry.number : '', guideChannelId: entry.guideChannelId, hidden: entry.hidden};
  const edit = (entry: ChannelMapPage['items'][number], patch: Partial<ChannelMapEdit>) => setEdits(current => ({...current, [entry.channelId]: {...value(entry.channelId, entry), ...patch}}));
  const changed = Object.keys(edits).length;
  const shown = page ? (filter.trim() ? page.items.filter(entry => `${entry.name} ${entry.number} ${entry.group}`.toLowerCase().includes(filter.trim().toLowerCase())) : page.items) : [];
  const save = async () => {
    if (!source || !changed) return;
    setBusy(true); setError(undefined);
    try {
      await controller.saveChannelMap(source.id, Object.entries(edits).map(([channelId, override]) => ({channelId, override})));
      setEdits({}); setBusy(false);
      await load(source.id);
    } catch (failure) {
      const partial = failure as {saved?: number; total?: number};
      setBusy(false); setError(partial.saved !== undefined ? Object.assign(new Error('partial'), {saved: partial.saved, total: partial.total}) : failure);
      if (source) void load(source.id);
    }
  };
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button ref={close} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
    <Button variant="primary" loading={busy} disabled={!changed} onPress={() => void save()}>{changed ? t('web.liveSource.saveMapChanges', {changed}) : t('action.save')}</Button>
  </View>;
  const partial = error as {saved?: number; total?: number} | undefined;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.channelMapTitle', {name: source?.name ?? ''})} description={t('web.liveSource.channelMapLede')} initialFocusRef={close} footer={footer} testID="live-owner-channel-map">
    <View style={{gap: 12}}>
      {error !== undefined && (partial?.saved !== undefined
        ? <Text testID="live-owner-channel-map-partial" style={[shellType(13), {color: theme.colors.amber}]}>{t('web.liveSource.channelMapPartial', {saved: partial.saved ?? 0, total: partial.total ?? changed})}</Text>
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
                <Text style={[shellType(15, 700), {color: theme.colors.text}]} numberOfLines={1}>{entry.name}</Text>
                <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[`${t('server.live.map.fromSource')} ${entry.sourceNumber || '—'}`, entry.group].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12.5), {color: theme.colors.textTertiary}]} numberOfLines={1}>{part}</Text>)}</View>
              </View>
              <Switch testID={`live-owner-channel-hidden-${entry.channelId}`} accessibilityLabel={t('web.liveSource.hideChannel', {name: entry.name})} checked={current.hidden} onCheckedChange={hidden => edit(entry, {hidden})}/>
            </View>
            <View style={{flexDirection: 'row', gap: 8}}>
              <TextField testID={`live-owner-channel-number-${entry.channelId}`} containerStyle={{flex: 1}} label={t('server.live.map.number')} value={current.number} maxLength={12} onChangeText={number => edit(entry, {number})}/>
              <TextField testID={`live-owner-channel-guide-${entry.channelId}`} containerStyle={{flex: 2}} label={t('server.live.map.guideId')} placeholder={t('player.quality.automatic')} value={current.guideChannelId} maxLength={200} autoCapitalize="none" autoCorrect={false} onChangeText={guideChannelId => edit(entry, {guideChannelId})}/>
            </View>
          </View>;
        })}
        {!loading && page && !shown.length && <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t('web.liveSource.noChannels')}</Text>}
        </>
        </LoadingBoundary>
      </ScrollView>
      {page?.nextCursor ? <Button variant="glass" size="small" loading={loading} onPress={() => {if (source && page) void load(source.id, page.nextCursor);}}>{t('action.showMoreChannels')}</Button> : null}
    </View>
  </Dialog>;
}
