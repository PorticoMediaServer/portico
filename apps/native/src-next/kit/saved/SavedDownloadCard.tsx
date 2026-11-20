import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Artwork} from '../artwork';
import {ArtFrame, CardFrame, Progress} from '../cards/primitives';
import {useTheme} from '../theme';
import type {NativeOfflineDownloadsDevice, OfflineGroup} from '../../platform/offline-downloads';
import {downloadEntryIdentity, downloadTransferProgress} from '../../screens/downloads/presentation';
import {useDownloadArtwork} from '../../screens/downloads/useDownloadArtwork';

export type NativeSavedDownloadsCopy = Readonly<{downloading: MessageId}>;

/** A shelf card with the local backdrop and room for the complete title and download status. */
export function SavedDownloadCard({group, device, copy, onOpen}: {group: OfflineGroup; device: NativeOfflineDownloadsDevice; copy: NativeSavedDownloadsCopy; onOpen: () => void}) {
  const theme = useTheme(), first = group.entries[0];
  const facts = useDownloadArtwork(device, first);
  const title = group.title || first.title, width = 214, height = width * 9 / 16, radius = theme.spec.card.radius[theme.column];
  const source = facts?.backdropUrl ? {uri: facts.backdropUrl, cacheKey: JSON.stringify([downloadEntryIdentity(first), facts.backdropUrl])} : undefined;
  const singleTransfer = group.entries.length === 1 && first.state === 'downloading' && !first.waiting;
  const progress = singleTransfer ? downloadTransferProgress(first) : undefined;
  const size = group.entries.reduce((sum, entry) => sum + (entry.state === 'ready' ? entry.bytes : entry.bytesDone), 0);
  const episodes = group.title && group.entries.every(entry => entry.info?.kind === 'episode') ? group.entries.length : undefined;
  return <CardFrame testID={`saved-download-${encodeURIComponent(group.key)}`} title={title} width={width} onNavigate={onOpen}>
    {interaction => <>
      <ArtFrame height={height} radius={radius} focused={interaction.focused} hovered={interaction.hovered} holding={interaction.holding}>
        <Artwork source={source} width={width} height={height}/>
      </ArtFrame>
      <View style={{marginTop: theme.space.xs}}>
        <Text style={[theme.type.wideTitle, {color: theme.colors.text}]}>{title}</Text>
        {progress !== undefined ? <>
          <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{defaultI18n.t(copy.downloading, {percent: Math.round(progress * 100)})}</Text>
          <Progress value={progress} height={4} style={{marginTop: 10}}/>
        </> : <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', columnGap: theme.space.sm}}>
          {episodes !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{defaultI18n.t('count.episodes', {count: episodes})}</Text>}
          <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{defaultI18n.bytes(size)}</Text>
        </View>}
      </View>
    </>}
  </CardFrame>;
}
