import React, {useId} from 'react';
import {Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Artwork} from '../../kit/artwork';
import {ArtFrame, CardFrame, Progress} from '../../kit/cards/primitives';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeOfflineDownloadsDevice, OfflineGroup} from '../../platform/offline-downloads';
import {downloadEntryIdentity, downloadTransferProgress} from '../downloads/presentation';
import {useDownloadArtwork} from '../downloads/useDownloadArtwork';

export type NativeSavedDownloadsCopy = Readonly<{downloading: MessageId}>;

/** phone9 uses a real cached local backdrop and its own drawn caption. A generic
 * continuation card intentionally filters these facts out on phones. */
export function SavedDownloadCard({group, device, copy, onOpen}: {group: OfflineGroup; device: NativeOfflineDownloadsDevice; copy: NativeSavedDownloadsCopy; onOpen: () => void}) {
  const theme = useTheme(), gradient = useId(), first = group.entries[0];
  const facts = useDownloadArtwork(device, first);
  // phone-app9 draws Saved > Downloads at a fixed 214 wide, 16:9, with the
  // shared card radius and the drawn on-art caption inset.
  const title = group.title || first.title, width = 214, height = width * 9 / 16, radius = theme.spec.card.radius[theme.platform];
  const source = facts?.backdropUrl ? {uri: facts.backdropUrl, cacheKey: JSON.stringify([downloadEntryIdentity(first), facts.backdropUrl])} : undefined;
  const singleTransfer = group.entries.length === 1 && first.state === 'downloading' && !first.waiting;
  const progress = singleTransfer ? downloadTransferProgress(first) : undefined;
  const size = group.entries.reduce((sum, entry) => sum + (entry.state === 'ready' ? entry.bytes : entry.bytesDone), 0);
  const episodes = group.title && group.entries.every(entry => entry.info?.kind === 'episode') ? group.entries.length : undefined;
  return <CardFrame testID={`saved-download-${encodeURIComponent(group.key)}`} title={title} width={width} onNavigate={onOpen}>
    {interaction => <ArtFrame height={height} radius={radius} focused={interaction.focused} hovered={interaction.hovered} holding={interaction.holding}>
      <Artwork source={source} width={width} height={height}/>
      <Svg pointerEvents="none" width={width} height={height} style={{position: 'absolute', inset: 0}}><Defs>
        <LinearGradient id={gradient} x1="0" y1="0" x2="0" y2="1"><Stop offset="0.2" stopColor={theme.colors.ink} stopOpacity={.05}/><Stop offset="0.78" stopColor={theme.colors.ink} stopOpacity={.88}/></LinearGradient>
      </Defs><Rect width={width} height={height} fill={`url(#${gradient})`}/></Svg>
      <View style={{position: 'absolute', left: 14, right: 14, bottom: 14}}>
        <Text numberOfLines={1} style={[theme.type.wideTitle, {color: theme.colors.text}]}>{title}</Text>
        {progress !== undefined ? <>
          <Text numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary, marginTop: 2}]}>{defaultI18n.t(copy.downloading, {percent: Math.round(progress * 100)})}</Text>
          <Progress value={progress} height={4} style={{marginTop: 10}}/>
        </> : <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 12, marginTop: 2}}>
          {episodes !== undefined && <Text numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary}]}>{defaultI18n.t('count.episodes', {count: episodes})}</Text>}
          <Text numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary}]}>{defaultI18n.bytes(size)}</Text>
        </View>}
      </View>
    </ArtFrame>}
  </CardFrame>;
}
