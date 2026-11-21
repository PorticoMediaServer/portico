import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {NowPlayingQueueProps, useNowPlayingQueue} from '../../pages/shared/player/useNowPlayingQueue';
import {Artwork} from '../artwork';
import {Focusable} from '../controls';
import {FocusRegion} from '../focus';
import {Menu} from '../overlays';
import {useTheme} from '../theme';

const t = defaultI18n.t, spec = frameSpec.audio.tv;

export function TVQueueRows({model, testID, interactive, regionId, edges, artwork}: {
  model: ReturnType<typeof useNowPlayingQueue>; testID: string; interactive: boolean;
  regionId?: string; edges?: NowPlayingQueueProps['edges']; artwork?: NowPlayingQueueProps['artwork'];
}) {
  const theme = useTheme(), {tvUpcoming, tvSummary, entry, songMenu, labels} = model;
  const rows = <ScrollView contentContainerStyle={{paddingBottom: theme.space.sm}} showsVerticalScrollIndicator={false}>
    {!!tvUpcoming.length && !!tvSummary.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm, marginBottom: theme.space.sm}}>
      {tvSummary.map((part, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{part}</Text>)}
    </View>}
    {tvUpcoming.map((row, index) => {
      const source = artwork?.(row.posterUrl), canHold = songMenu.canOpen(row);
      const group = row.segmentId ? labels.get(row.segmentId) : undefined;
      const showGroup = group && (index === 0 || row.segmentId !== tvUpcoming[index - 1]?.segmentId);
      return <React.Fragment key={row.entryId}>
        {showGroup && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: theme.space.sm}]}>{group}</Text>}
        <Focusable testID={`${testID}-row-${row.entryId}`} focusId={`${testID}-${row.entryId}`} focusKind="row" focusRadius={10}
          accessibilityRole="button" accessibilityLabel={t('player.queuePlay', {title: row.title})}
          disabled={!interactive || !model.canPlay && !canHold} focusableWhenDisabled={interactive}
          onFocus={() => model.remember(row.entryId)} onPress={model.canPlay && interactive ? () => model.play(row.entryId) : undefined}
          onLongPressAction={canHold ? () => songMenu.open(row) : undefined}
          style={({focused}) => ({minHeight: spec.row, paddingVertical: theme.space.tight, paddingHorizontal: theme.space.sm, flexDirection: 'row',
            alignItems: 'center', gap: theme.space.sm, borderRadius: theme.baseRadii.control, backgroundColor: focused ? theme.colors.raisedHigh : 'transparent'})}>
          {source && <Artwork source={source} width={spec.rowArt} height={spec.rowArt} style={{borderRadius: spec.rowArtRadius, overflow: 'hidden', flexShrink: 0}}/>}
          <View style={{flex: 1, minWidth: 0}}>
            <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{row.title}</Text>
            {row.artist && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{row.artist}</Text>}
          </View>
          {row.current && <Text style={[theme.type.caption, {color: theme.colors.accentStrong}]}>{t('player.queueNowPlaying')}</Text>}
          {!!row.seconds && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{clockText(row.seconds)}</Text>}
        </Focusable>
      </React.Fragment>;
    })}
    {!tvUpcoming.length && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('player.queueEmpty')}</Text>}
  </ScrollView>;
  return <View testID={testID} style={{flex: 1, minHeight: 0}}>
    {regionId ? <FocusRegion id={regionId} enabled={interactive} entry={entry ? `${testID}-${entry}` : undefined}
      order={tvUpcoming.map(row => `${testID}-${row.entryId}`)} orientation="vertical" edges={edges} style={{flex: 1}}>{rows}</FocusRegion> : rows}
    {/* Keep the mounted menu through close so its exit restores the original focus. */}
    <Menu testID={`${testID}-song-menu`} presentation="sideSheet" {...songMenu.menu}/>
  </View>;
}
