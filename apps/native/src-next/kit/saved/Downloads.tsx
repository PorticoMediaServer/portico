import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {useFrameMetrics} from '../frames/metrics';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {frameSpec} from '@portico/design';

/** A download's poster beside its words (tablet 11b, collections-34): 2:3 at this width. */
const downloadArt = frameSpec.rowArt.download;

export type StorageMeterProps = Readonly<{
  /** "3.4 GB used", said strongly, then "41 GB free on this iPad". */
  used: string; free?: string;
  /** The share used, and an optional second share in a quieter tone (other profiles, recordings kept). */
  value: number; other?: number;
  /** One line under the bar: "About 230 hours of recording space left". */
  note?: string;
  onPress?: () => void;
}>;

/** How much room is used (tablet 11b Downloads, desktop-live live-upcoming Recordings, phone-app 12): a raised card with
 * the figures, the bar and a line. */
export function StorageMeter({used, free, value, other, note, onPress}: StorageMeterProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable accessibilityLabel={[used, free].filter(Boolean).join(', ')} onPress={onPress} disabled={!onPress} focusableWhenDisabled={false}
    style={{marginHorizontal: frame.cardInset, padding: frame.cardPadding, gap: theme.space.xs, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.textSecondary}]}><Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{used}</Text>{free !== undefined ? `   ${free}` : ''}</Text>
    <View style={{height: 6, borderRadius: 3, flexDirection: 'row', overflow: 'hidden', backgroundColor: alpha(theme.colors.text, .12)}}>
      <View style={{width: `${Math.round(value * 100)}%`, backgroundColor: theme.colors.accentStrong}}/>
      {other !== undefined && <View style={{width: `${Math.round(other * 100)}%`, backgroundColor: alpha(theme.colors.accentStrong, .4)}}/>}
    </View>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
  </Focusable>;
}

export type DownloadRowProps = Readonly<{
  title: string;
  /** "S1 E1 · Original · 1.2 GB". */
  detail?: string;
  artwork?: ArtworkSource;
  /** Where it is: "Downloading 64% · about 4 min left", "Waiting for Wi-Fi", "The download stopped." */
  status?: Readonly<{text: string; tone?: 'quiet' | 'warning' | 'error' | 'ready'}>;
  progress?: number;
  /** The row's one action: Play on a ready one, Try again on a failed one. A ⋯ follows unless it is TV. */
  action?: ReactNode;
  /** A failed one's Try again, under where it is (tablet 11b). */
  retry?: Readonly<{label: string; onPress: () => void}>;
  more?: Readonly<{label: string; onPress: () => void}>;
  /** A rule between this row and the one above it, inside one card. */
  separator?: boolean;
  onPress?: () => void;
}>;

/** One download (tablet 11b, collections-34 desktop Downloads, collections-36 mobile web): the poster, the title and
 * its facts, where the download is, its bar, and the action that fits. The real Downloads (`screens/downloads`) still
 * draws its own row; Stage 2 moves it onto this part (FRAMES.md). */
export function DownloadRow({title, detail, artwork, status, progress, action, retry, more, separator = false, onPress}: DownloadRowProps) {
  const theme = useTheme(), frame = useFrameMetrics(), width = downloadArt[frame.form];
  const tone = status?.tone === 'warning' ? theme.colors.amber : status?.tone === 'error' ? theme.colors.record : status?.tone === 'ready' ? theme.colors.healthy : theme.colors.textSecondary;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: frame.cardPadding, paddingVertical: theme.space.xs, borderTopWidth: separator ? 1 : 0, borderColor: theme.colors.line}}>
    <Focusable accessibilityLabel={title} onPress={onPress} style={{flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <View style={{width, height: width * 1.5, borderRadius: theme.baseRadii.small, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh}}><Artwork source={artwork} width={width} height={width * 1.5}/></View>
      <View style={{flex: 1, minWidth: 0, gap: 2}}>
        <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
        {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
        {status && <Text style={[theme.type.rowHelp, {color: tone}]}>{status.text}</Text>}
        {progress !== undefined && <View style={{height: 3, borderRadius: 2, marginTop: theme.space.tight, backgroundColor: alpha(theme.colors.text, .14)}}><View style={{width: `${Math.round(progress * 100)}%`, height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View>}
        {retry && <View style={{marginTop: theme.space.tight}}><Button variant="glass" size="small" icon="i-retry" onPress={retry.onPress}>{retry.label}</Button></View>}
      </View>
    </Focusable>
    {action}
    {more && frame.form !== 'tv' && <Button variant="icon" size="small" icon="i-more" iconOnly accessibilityLabel={more.label} onPress={more.onPress}/>}
  </View>;
}
