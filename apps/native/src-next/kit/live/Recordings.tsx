import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Artwork, ChannelMark, type ArtworkSource} from '../artwork';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';
import {Flag, guideSpec, type GuideChannel} from './Guide';

/** A recording's picture beside its words: 16:9 at this width from tablet width; a phone's row has none (phone-app 12). R5 interim. */
const recordingArt = {phone: 0, medium: 132, expanded: 160, tv: 220} as const; // R5 interim

export type RecordingRowProps = Readonly<{
  /** The start and the time range, in the row's first column. */
  time: string; range?: string;
  title: string;
  /** New, Live: chips before the episode line. */
  flags?: readonly Readonly<{id: 'live' | 'new' | 'premiere'; text: string}>[];
  /** "S5 E9  Crossed Wires", or the year. */
  detail?: string;
  channel?: Omit<GuideChannel, 'cells'>;
  /** "Series rule", "One time". */
  source?: string;
  artwork?: ArtworkSource;
  /** "Recording", "Tuner conflict", "Series": said in the row's status column. */
  status?: Readonly<{text: string; tone?: 'record' | 'accent'}>;
  onPress?: () => void;
  more?: Readonly<{label: string; onPress: () => void}>;
}>;

/** One upcoming or recording-now row (desktop-live live-upcoming, phone-app 12). */
export function RecordingRow({time, range, title, flags, detail, channel, source, artwork, status, onPress, more}: RecordingRowProps) {
  const theme = useTheme(), frame = useFrameMetrics(), art = recordingArt[frame.form];
  const tone = status?.tone === 'record' ? theme.colors.record : status?.tone === 'accent' ? theme.colors.accentStrong : theme.colors.textSecondary;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, paddingVertical: theme.space.xs}}>
    <View style={{width: guideSpec.timeColumn[frame.form] + (art ? theme.space.md : 0)}}>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{time}</Text>
      {range !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{range}</Text>}
    </View>
    <Focusable accessibilityLabel={title} onPress={onPress} style={{flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      {art > 0 && <View style={{width: art, height: art * 9 / 16, borderRadius: theme.radii.artwork, overflow: 'hidden', backgroundColor: theme.colors.raised}}><Artwork source={artwork} width={art} height={art * 9 / 16}/></View>}
      <View style={{flex: 1, minWidth: 0, gap: 2}}>
        <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
        {(flags?.length || detail !== undefined) && <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.tight}}>
          {flags?.map(flag => <Flag key={flag.id} flag={flag}/>)}
          {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{detail}</Text>}
        </View>}
        {(channel || source !== undefined) && <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.tight}}>
          {channel && <><ChannelMark mark={channel.mark} size={guideSpec.mark.phone / 2}/><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{channel.name}{channel.number !== undefined && frame.form === 'phone' ? `  ${channel.number}` : ''}</Text></>}
          {source !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{source}</Text>}
        </View>}
      </View>
    </Focusable>
    {status && <Text style={[theme.type.rowHelp, {color: tone, fontFamily: theme.type.listLabel.fontFamily}]}>{status.tone === 'record' ? '● ' : ''}{status.text}</Text>}
    {more && frame.form !== 'tv' && <Button variant="icon" size="small" icon="i-more" iconOnly accessibilityLabel={more.label} onPress={more.onPress}/>}
  </View>;
}

export type RuleCardProps = Readonly<{
  title: string;
  /** What it records, where, what it keeps: "All episodes · Any channel · Keep every episode". */
  summary: string;
  /** "Next: today, 4:15 PM on Atlas Documentaries"; `warning` says it in the record colour (a tuner conflict). */
  next?: string; warning?: boolean;
  /** On or Paused, and how many airings are coming. */
  state: Readonly<{label: string; on: boolean}>; upcoming?: string;
  artwork?: ArtworkSource;
  selected?: boolean;
  onPress?: () => void;
  more?: Readonly<{label: string; onPress: () => void}>;
}>;

/** One series rule (desktop-live live-rules): its picture, what it records, what is next, On or Paused. The web kit's `dvr` RuleCard is the same part (it draws a generated picture from `glyph`). */
export function RuleCard({title, summary, next, warning = false, state, upcoming, artwork, selected = false, onPress, more}: RuleCardProps) {
  const theme = useTheme(), frame = useFrameMetrics(), art = recordingArt[frame.form];
  return <Focusable accessibilityLabel={title} onPress={onPress} style={{marginHorizontal: frame.cardInset, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, padding: frame.cardPadding / 2 + theme.space.tight, borderRadius: frame.cardRadius,
    backgroundColor: theme.colors.raised, borderWidth: selected ? 2 : 0, borderColor: theme.colors.accentStrong, opacity: state.on ? 1 : .72}}>
    {art > 0 && <View style={{width: art * .9, height: art * .9 * 9 / 16, borderRadius: theme.radii.artwork, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh}}><Artwork source={artwork} width={art * .9} height={art * .9 * 9 / 16}/></View>}
    <View style={{flex: 1, minWidth: 0, gap: 2}}>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{summary}</Text>
      {next !== undefined && <Text style={[theme.type.rowHelp, {color: warning ? theme.colors.record : theme.colors.textTertiary}]}>{next}</Text>}
    </View>
    <View style={{alignItems: 'center', gap: theme.space.tight}}>
      <View style={{paddingHorizontal: theme.space.xs, paddingVertical: 2, borderRadius: theme.baseRadii.small, backgroundColor: state.on ? theme.colors.raisedBright : theme.colors.raisedHigh}}>
        <Text style={[theme.type.rowHelp, {color: state.on ? theme.colors.healthy : theme.colors.textSecondary, fontFamily: theme.type.listLabel.fontFamily}]}>{state.label}</Text>
      </View>
      {upcoming !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{upcoming}</Text>}
    </View>
    {more && frame.form !== 'tv' && <Button variant="icon" size="small" icon="i-more" iconOnly accessibilityLabel={more.label} onPress={more.onPress}/>}
  </Focusable>;
}

export type ConflictCardProps = Readonly<{title: string; body: string; children?: ReactNode; action?: Readonly<{label: string; onPress: () => void}>}>;

/** Recordings' conflict (desktop-live live-upcoming, phone-app 12): a card ruled in the record colour that says what
 * clashes and why, the airings involved, and Resolve conflict. */
export function ConflictCard({title, body, children, action}: ConflictCardProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{marginHorizontal: frame.cardInset, padding: frame.cardPadding, gap: theme.space.sm, borderRadius: frame.cardRadius, borderWidth: 1, borderColor: theme.colors.record, backgroundColor: theme.colors.raised}}>
    <View style={{flexDirection: 'row', gap: theme.space.xs, alignItems: 'flex-start'}}>
      <Icon name="i-warn" size={20} color={theme.colors.record}/>
      <View style={{flex: 1, gap: 2}}>
        <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{body}</Text>
      </View>
    </View>
    {children}
    {action && <Button variant="glass" size="small" onPress={action.onPress}>{action.label}</Button>}
  </View>;
}
