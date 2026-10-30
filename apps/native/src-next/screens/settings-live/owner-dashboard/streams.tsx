import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Text, View} from 'react-native';
import {ProfileFace, type ArtworkSource} from '../../../kit/artwork';
import {SessionCard} from '../../../kit/cards';
import {Focusable} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import {alpha, shellType} from '../../../kit/shell/visual';
import {OWNER_COMPACT_ROW_PX, type OwnerStreamTotal, type OwnerStreamView, type Translate} from './model';
import {OwnerCalm} from './parts';

export type OwnerStreamEntry = Readonly<{view: OwnerStreamView; artwork?: ArtworkSource}>;

const faceColors = ['mint', 'violet', 'gold', 'blue', 'slate', 'rose', 'sky'] as const;

function faceColor(index: number) {
  return faceColors[index % faceColors.length];
}

/** The frame's initial circle: the stream's own person, tinted by its place in the list. */
function face(index: number, name: string, size = 20) {
  return <ProfileFace initial={(name.trim()[0] ?? '?').toUpperCase()} color={faceColor(index)} size={size} scaleOnFocus={false}/>;
}

/** The drawn Now playing header; the page repeats it under the bar while the expanded list scrolls. */
export function OwnerNowPlayingHeader({t, people, count, expanded, onShowFewer, onLayout, hidden, sticky}: {
  t: Translate;
  people?: string;
  count: number;
  expanded: boolean;
  onShowFewer: () => void;
  onLayout?: (event: {nativeEvent: {layout: {y: number}}}) => void;
  hidden?: boolean;
  sticky?: boolean;
}) {
  const theme = useTheme();
  return <View onLayout={onLayout} accessibilityElementsHidden={hidden} importantForAccessibility={hidden ? 'no-hide-descendants' : 'auto'}
    style={sticky ? {paddingHorizontal: 20, paddingVertical: 10} : {marginTop: 32, paddingHorizontal: 16}}>
    <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 10, paddingHorizontal: sticky ? 0 : 4, marginBottom: sticky ? 0 : 12}}>
      <Text accessibilityRole="header" style={[shellType(20, 700, 'normal', -.015), {color: theme.colors.text}]}>{t('settings.server.nowPlaying')}</Text>
      <Text numberOfLines={1} style={[shellType(13, 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>{expanded ? t('next.dashboard.streams', {count}) : people}</Text>
      {expanded ? <View style={{marginLeft: 'auto'}}>
        <Focusable testID={sticky ? 'owner-stream-show-fewer-sticky' : 'owner-stream-show-fewer'} accessibilityRole="button" accessibilityLabel={t('next.dashboard.fewer')} onPress={onShowFewer} focusKind="row" focusRadius={8}
          style={{height: 32, paddingHorizontal: 10, borderRadius: 8, alignItems: 'center', justifyContent: 'center'}}>
          <Text style={[shellType(13.5, 700), {color: theme.colors.textSecondary}]}>{t('next.dashboard.fewer')}</Text>
        </Focusable>
      </View> : null}
    </View>
  </View>;
}

/** Now playing as the frames draw it at ten streams or fewer: one authorized card per stream. */
export function OwnerStreamCards({streams, loading, error, onRetry, onStop, onMenu, t}: {
  streams: readonly OwnerStreamEntry[];
  loading: boolean;
  error?: unknown;
  onRetry: () => void;
  onStop: (view: OwnerStreamView) => void;
  onMenu: (view: OwnerStreamView) => void;
  t: Translate;
}) {
  return <View style={{paddingHorizontal: 16}}>
    {error !== undefined && !streams.length ? <OwnerCalm icon="i-warn" tone="error" title={t('next.shell.activityError')} action={{label: t('next.shell.tryAgain'), onPress: onRetry}}/> : null}
    {!loading && error === undefined && !streams.length ? <OwnerCalm icon="i-play" title={t('web.nowPlaying.empty')} body={t('next.dashboard.calmStreamsBody')}/> : null}
    {streams.map((entry, index) => <View key={entry.view.id} style={{marginBottom: 12}}>
      <SessionCard testID={`owner-stream-${entry.view.id}`} title={entry.view.title} identity={entry.view.identity} facts={[...entry.view.facts, ...(entry.view.state ? [entry.view.state] : []), ...(entry.view.position ? [entry.view.position] : [])]}
        viewer={entry.view.viewer} viewerBadge={face(index, entry.view.viewer.username || entry.view.viewer.name)} artwork={entry.artwork} progress={entry.view.progress} paused={entry.view.paused}
        primaryAction="play" onPlay={() => onMenu(entry.view)} onStop={() => onStop(entry.view)} onMore={() => onMenu(entry.view)} accessibilityLabel={`${entry.view.title}, ${entry.view.viewer.username || entry.view.viewer.name}, ${entry.view.viewer.device}`} menuVisibility="always"/>
    </View>)}
  </View>;
}

/** The three totals first, counting every stream the server reports, paused ones included. */
export function OwnerStreamTotals({totals}: {totals: readonly OwnerStreamTotal[]}) {
  const theme = useTheme();
  return <View testID="owner-stream-totals" style={{flexDirection: 'row', gap: 2, marginHorizontal: 16, marginBottom: 12, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.lineSoft}}>
    {totals.map(tile => <View key={tile.id} style={{flex: 1, minWidth: 0, backgroundColor: theme.colors.raised, paddingHorizontal: 12, paddingVertical: 12}}>
      <Text numberOfLines={1} style={[shellType(12.5, 600), {color: theme.colors.textTertiary}]}>{tile.label}</Text>
      <View style={{flexDirection: 'row', alignItems: 'baseline', marginTop: 2}}>
        <Text numberOfLines={1} style={[shellType(26, 800, 'normal', -.03), {color: tile.tone === 'converting' ? theme.colors.accentStrong : theme.colors.text, fontVariant: ['tabular-nums']}]}>{tile.value}</Text>
        {tile.unit ? <Text style={[shellType(13, 700), {color: theme.colors.textTertiary, marginLeft: 3}]}>{tile.unit}</Text> : null}
      </View>
      {tile.note ? <Text numberOfLines={1} style={[shellType(12, 600), {color: theme.colors.textTertiary, marginTop: 1}]}>{tile.note}</Text> : null}
    </View>)}
  </View>;
}

/**
 * One compact row: who on the first line (username in bold, the profile only when it differs,
 * then the device), what on the second, a note only when notable, and how far in underneath.
 * No artwork and no Stop: the row or its ⋯ opens the stream menu.
 */
export function OwnerCompactRow({entry, index, t, onMenu}: {entry: OwnerStreamEntry; index: number; t: Translate; onMenu: () => void}) {
  const theme = useTheme(), view = entry.view;
  const username = view.viewer.username || view.viewer.name, profile = view.viewer.username && view.viewer.name !== view.viewer.username ? view.viewer.name : undefined;
  const qualifier = view.qualifier;
  const note = view.note;
  const progress = view.progress;
  return <Focusable testID={`owner-stream-row-${view.id}`} accessibilityRole="button" accessibilityLabel={defaultI18n.list([view.title, username, ...(profile ? [profile] : []), view.viewer.device])}
    onPress={onMenu} focusKind="row" focusRadius={14}
    style={state => ({height: OWNER_COMPACT_ROW_PX, paddingLeft: 14, paddingRight: 8, backgroundColor: state.pressed ? alpha(theme.colors.text, .05) : 'transparent'})}>
    <View style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: 12}}>
      {face(index, username, 32)}
      <View style={{flex: 1, minWidth: 0}}>
        <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 8}}>
          <Text numberOfLines={1} style={[shellType(14.5, 800), {color: theme.colors.text, flexShrink: 1}]}>{username}</Text>
          {profile ? <Text numberOfLines={1} style={[shellType(13, 600), {color: theme.colors.textTertiary, flexShrink: 1000}]}>{profile}</Text> : null}
          <Text numberOfLines={1} style={[shellType(13, 600), {color: theme.colors.textTertiary, flexShrink: 100000}]}>{view.viewer.device}</Text>
        </View>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: 1}}>
          <Text numberOfLines={1} style={[shellType(13, 400), {color: theme.colors.textSecondary, flexShrink: 1}]}>{view.title}{qualifier ? <Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{`  ${qualifier}`}</Text> : null}</Text>
          {note ? <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginLeft: 'auto', flexShrink: 0}}>
            {note.tone === 'paused' ? <Icon name="i-pause" size={11} color={theme.colors.textTertiary}/>
              : <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: theme.colors.amber, ...(note.tone === 'problem' ? {shadowColor: theme.colors.amber, shadowOpacity: .32, shadowRadius: 3, shadowOffset: {width: 0, height: 0}} : {})}}/>}
            <Text style={[shellType(12.5, 700), {color: note.tone === 'problem' ? theme.colors.amber : note.tone === 'route' ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{note.text}</Text>
          </View> : null}
        </View>
      </View>
      <Icon name="i-more" size={18} color={theme.colors.textTertiary}/>
    </View>
    {progress !== undefined ? <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 2, backgroundColor: alpha(theme.colors.text, .08)}}>
      <View style={{height: '100%', width: `${Math.round(progress * 1000) / 10}%`, backgroundColor: view.paused ? theme.colors.textTertiary : theme.colors.accentStrong}}/>
    </View> : null}
  </Focusable>;
}

/**
 * The compact rows, windowed at the row's fixed pitch: the spacers keep the true scroll extent,
 * so hundreds of streams mount only the rows in view and a few either side.
 */
export function OwnerCompactRows({streams, window, onMenu, t}: {
  streams: readonly OwnerStreamEntry[];
  window: {first: number; last: number};
  onMenu: (view: OwnerStreamView) => void;
  t: Translate;
}) {
  const theme = useTheme();
  const first = Math.max(0, Math.min(window.first, streams.length)), last = Math.min(streams.length - 1, window.last);
  return <View testID="owner-stream-rows" style={{marginHorizontal: 16, borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>
    {first > 0 ? <View style={{height: first * OWNER_COMPACT_ROW_PX}}/> : null}
    {streams.slice(first, last + 1).map((entry, offset) => <OwnerCompactRow key={entry.view.id} entry={entry} index={first + offset} t={t} onMenu={() => onMenu(entry.view)}/>)}
    {last < streams.length - 1 ? <View style={{height: (streams.length - 1 - last) * OWNER_COMPACT_ROW_PX}}/> : null}
  </View>;
}

/** Show all {count} under the tenth row, or Show fewer at the end of the expanded list. */
export function OwnerShowAll({expanded, count, onPress, t}: {expanded: boolean; count: number; onPress: () => void; t: Translate}) {
  const theme = useTheme(), label = expanded ? t('next.dashboard.fewer') : t('next.dashboard.showAll', {count});
  return <Focusable testID={expanded ? 'owner-stream-show-fewer-end' : 'owner-stream-show-all'} accessibilityRole="button" accessibilityLabel={label} onPress={onPress} focusKind="row" focusRadius={12}
    style={{height: 46, marginHorizontal: 16, marginTop: 12, borderRadius: 12, backgroundColor: alpha(theme.colors.text, .08), flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6}}>
    <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{label}</Text>
    <Icon name={expanded ? 'i-up' : 'i-down'} size={18} color={theme.colors.text}/>
  </Focusable>;
}
