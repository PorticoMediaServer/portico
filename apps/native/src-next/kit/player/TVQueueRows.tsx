import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NowPlayingQueueProps, useNowPlayingQueue} from '../../pages/shared/player/useNowPlayingQueue';
import {QueueGroupLabel, QueueRow} from './QueueRow';
import {FocusRegion} from '../focus';
import {Menu} from '../overlays';
import {useTheme} from '../theme';

const t = defaultI18n.t;

export function TVQueueRows({model, testID, interactive, regionId, edges, artwork}: {
  model: ReturnType<typeof useNowPlayingQueue>; testID: string; interactive: boolean;
  regionId?: string; edges?: NowPlayingQueueProps['edges']; artwork?: NowPlayingQueueProps['artwork'];
}) {
  const theme = useTheme(), {tvUpcoming, tvSummary, entry, songMenu, labels} = model;
  const rows = <ScrollView contentContainerStyle={{paddingBottom: theme.space.sm}} showsVerticalScrollIndicator={false}>
    {!!tvUpcoming.length && !!tvSummary.length && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginBottom: theme.space.sm}]}>
      {tvSummary.join(' · ')}
    </Text>}
    {tvUpcoming.map((row, index) => {
      const canHold = songMenu.canOpen(row);
      const group = row.segmentId ? labels.get(row.segmentId) : undefined;
      const showGroup = group && (index === 0 || row.segmentId !== tvUpcoming[index - 1]?.segmentId);
      return <React.Fragment key={row.entryId}>
        {showGroup && <QueueGroupLabel>{group}</QueueGroupLabel>}
        <QueueRow testID={`${testID}-row-${row.entryId}`} focusId={`${testID}-${row.entryId}`}
          title={row.title} detail={row.artist ? [row.artist] : undefined} seconds={row.seconds}
          source={artwork?.(row.posterUrl) ?? undefined} current={row.current}
          disabled={!interactive || !model.canPlay && !canHold} focusableWhenDisabled={interactive}
          onFocus={() => model.remember(row.entryId)}
          onPress={model.canPlay && interactive ? () => model.play(row.entryId) : undefined}
          onLongPress={canHold ? () => songMenu.open(row) : undefined}/>
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
