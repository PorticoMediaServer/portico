import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {ChannelMark} from '../artwork';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';
import {TVRow} from '../layout/TVRow';
import {frameSpec} from '@portico/design';

/** The guide's measurements per form: `frameSpec.guide`. */
export const guideSpec = frameSpec.guide;

export type GuideChannel = Readonly<{
  id: string; name: string; number?: string; favourite?: boolean;
  mark: Readonly<{sourceId: string; id: string; text: string; seed: string}>;
  cells: readonly GuideCell[];
}>;
/** One programme, placed by minutes from the guide's left edge (`from` below 0 is a programme that began earlier). */
export type GuideCell = Readonly<{
  id: string; title: string; detail?: string; from: number; to: number;
  /** Live, New, Premiere: small chips before the detail. */
  flags?: readonly Readonly<{id: 'live' | 'new' | 'premiere'; text: string}>[];
  /** Being recorded: a red dot before the title, and the word in the detail line. */
  recording?: string;
  /** Ended before now: drawn quieter. */
  past?: boolean;
  /** No guide information: a hatched cell that still opens the channel. */
  empty?: boolean;
  selected?: boolean;
  progress?: number;
}>;

export type GuideGridProps = Readonly<{
  channels: readonly GuideChannel[];
  /** How many minutes the grid spans, and where now is. */
  span: number; now: number;
  /** The ruler's labels, each at its minute. */
  ruler: readonly Readonly<{at: number; label: string}>[];
  nowLabel: string;
  channelLabel: string;
  onCell?: (channel: GuideChannel, cell: GuideCell) => void;
}>;

/** The Live TV guide grid (desktop-live live-guide, phone-guide g1, tablet-play 6, final3-tv-10): a channel column that
 * stays put, a time ruler with now marked on it, and one row of programmes per channel that scrolls sideways, crossed by
 * the now-line. The real guide (`screens/settings-live/LiveScreen.tsx`, web `screens/live/GuideScreen.tsx`) still draws
 * its own grid; Stage 2 moves it onto this part (FRAMES.md). */
export function GuideGrid({channels, span, now, ruler, nowLabel, channelLabel, onCell}: GuideGridProps) {
  const theme = useTheme(), frame = useFrameMetrics(), {form} = frame, px = guideSpec.perMinute[form], row = guideSpec.row[form];
  const width = span * px, height = channels.length * (row + guideSpec.gap);
  const phone = form === 'phone';
  // A ruler time the now pill would sit on is left out (phone-guide g1: 3:30, 4:05 now, 4:30), so no half-covered label
  // shows beside the pill. Reach in the ruler's text size: a label's width before now, the pill's half after it. Web: the same rule.
  const size = theme.type.rowHelp.fontSize ?? 13, underNow = (at: number) => (at - now) * px > -size * 6.5 && (at - now) * px < size * 2.4;
  return <View style={{flexDirection: 'row', paddingLeft: frame.gutter}}>
    <View style={{width: guideSpec.channel[form]}}>
      <View style={{height: guideSpec.ruler, justifyContent: 'center'}}>{!phone && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{channelLabel}</Text>}</View>
      {channels.map(channel => <View key={channel.id} style={{height: row, marginBottom: guideSpec.gap, flexDirection: phone ? 'column' : 'row', alignItems: phone ? 'flex-start' : 'center', justifyContent: 'center', gap: phone ? 2 : theme.space.sm}}>
        <ChannelMark mark={channel.mark} size={guideSpec.mark[form]}/>
        {phone ? channel.number !== undefined && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{channel.number}</Text>
          : <View style={{flex: 1, minWidth: 0}}>
            <Text numberOfLines={2} style={[theme.type.listLabel, {color: theme.colors.text}]}>{channel.name}{channel.favourite && <Text style={{color: theme.colors.accentStrong}}> ★</Text>}</Text>
            {channel.number !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{channel.number}</Text>}
          </View>}
      </View>)}
    </View>
    <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingRight: frame.rightGutter}}>
      <View style={{width, height: guideSpec.ruler + height}}>
        {ruler.filter(mark => !underNow(mark.at)).map(mark => <View key={mark.at} style={{position: 'absolute', left: mark.at * px, top: 0, height: guideSpec.ruler, justifyContent: 'center', paddingLeft: theme.space.xs, borderLeftWidth: 1, borderColor: theme.colors.line}}>
          <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{mark.label}</Text>
        </View>)}
        {channels.map((channel, index) => channel.cells.map(cell => {
          const left = Math.max(0, cell.from) * px, right = Math.min(span, cell.to) * px;
          return <Cell key={channel.id + cell.id} cell={cell} left={left} width={right - left - guideSpec.gap} top={guideSpec.ruler + index * (row + guideSpec.gap)} height={row} onPress={onCell && (() => onCell(channel, cell))}/>;
        }))}
        {/* The now-line, and its time in a pill on the ruler. */}
        <View pointerEvents="none" style={{position: 'absolute', left: now * px - 1, top: guideSpec.ruler / 2, width: 2, height: height + guideSpec.ruler / 2, backgroundColor: theme.colors.accentStrong}}/>
        <View pointerEvents="none" style={{position: 'absolute', left: now * px - guideSpec.nowPill / 2, width: guideSpec.nowPill, top: 0, height: guideSpec.ruler, alignItems: 'center', justifyContent: 'center'}}>
          <View style={{paddingHorizontal: theme.space.xs, paddingVertical: 2, borderRadius: guideSpec.radius / 2, backgroundColor: theme.colors.accentStrong}}>
            <Text style={[theme.type.cardSubtitle, {color: theme.colors.ink, fontFamily: theme.type.listLabel.fontFamily}]}>{nowLabel}</Text>
          </View>
        </View>
      </View>
    </ScrollView></TVRow>
  </View>;
}

function Cell({cell, left, width, top, height, onPress}: {cell: GuideCell; left: number; width: number; top: number; height: number; onPress?: () => void}) {
  const theme = useTheme(), frame = useFrameMetrics(), pad = frame.form === 'tv' ? theme.space.sm : theme.space.controlGap;
  const quiet = cell.past ? theme.colors.textTertiary : theme.colors.text;
  return <Focusable accessibilityLabel={cell.title} onPress={onPress} focusRadius={guideSpec.radius} style={{position: 'absolute', left, top, width, height, borderRadius: guideSpec.radius, overflow: 'hidden', paddingHorizontal: pad, justifyContent: 'center',
    backgroundColor: cell.empty ? alpha(theme.colors.raised, .5) : cell.selected ? theme.colors.raisedBright : theme.colors.raisedHigh, borderWidth: cell.selected ? 2 : 0, borderColor: theme.colors.accentStrong}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
      {cell.from < 0 && <Icon name="i-left" size={12} color={theme.colors.textTertiary}/>}
      {cell.recording !== undefined && <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.record}}/>}
      <Text numberOfLines={2} style={[theme.type.cardTitle, {flexShrink: 1, color: quiet}]}>{cell.title}</Text>
    </View>
    {(cell.detail !== undefined || cell.flags?.length || cell.recording) && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight, marginTop: 2}}>
      {cell.recording !== undefined && <Text numberOfLines={1} style={[theme.type.cardSubtitle, {color: theme.colors.record}]}>{cell.recording}</Text>}
      {cell.flags?.map(flag => <Flag key={flag.id} flag={flag}/>)}
      {cell.detail !== undefined && <Text numberOfLines={1} style={[theme.type.cardSubtitle, {flexShrink: 1, color: theme.colors.textTertiary}]}>{cell.detail}</Text>}
    </View>}
    {cell.progress !== undefined && <View style={{position: 'absolute', left: 0, right: 0, bottom: 0, height: 3, backgroundColor: alpha(theme.colors.text, .12)}}>
      <View style={{width: `${Math.round(cell.progress * 100)}%`, height: 3, backgroundColor: theme.colors.accentStrong}}/>
    </View>}
  </Focusable>;
}

/** Live, New, Premiere: Live is the record colour, New the accent, Premiere an accent outline (desktop-live live-guide). */
export function Flag({flag}: {flag: Readonly<{id: 'live' | 'new' | 'premiere'; text: string}>}) {
  const theme = useTheme(), live = flag.id === 'live', outline = flag.id === 'premiere';
  return <View style={{paddingHorizontal: 5, borderRadius: 4, borderWidth: outline ? 1 : 0, borderColor: theme.colors.accentStrong, backgroundColor: live ? alpha(theme.colors.record, .24) : outline ? 'transparent' : theme.colors.accent}}>
    <Text style={[theme.type.cardSubtitle, {color: live ? theme.colors.record : outline ? theme.colors.accentStrong : theme.colors.text, fontFamily: theme.type.listLabel.fontFamily}]}>{flag.text}</Text>
  </View>;
}

export type ChannelRowProps = Readonly<{
  channel: Omit<GuideChannel, 'cells'>;
  /** What is on now, how much is left, and its share played. */
  title: string; left?: string; progress?: number;
  /** "Live" beside the channel's name. */
  live?: string;
  /** "Next: Morning Briefing at 4:30 PM". */
  next?: string;
  onPress?: () => void;
}>;

/** The channel list (phone-app 10, desktop-live live-list): the channel's mark and number, its name, what is on with the
 * time left and a bar, then what is next. */
export function ChannelRow({channel, title, left, progress, live, next, onPress}: ChannelRowProps) {
  const theme = useTheme(), frame = useFrameMetrics(), mark = guideSpec.mark[frame.form] + 10;
  return <Focusable accessibilityLabel={title} onPress={onPress} style={{flexDirection: 'row', gap: theme.space.sm, paddingVertical: theme.space.sm, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, borderBottomWidth: 1, borderColor: theme.colors.line}}>
    <View style={{alignItems: 'center', gap: theme.space.tight}}>
      <ChannelMark mark={channel.mark} size={mark}/>
      {channel.number !== undefined && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{channel.number}</Text>}
    </View>
    <View style={{flex: 1, minWidth: 0, gap: 2}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
        <Text numberOfLines={1} style={[theme.type.rowHelp, {flexShrink: 1, color: theme.colors.textSecondary}]}>{channel.name}</Text>
        {live !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.record}]}>● {live}</Text>}
        <View style={{flex: 1}}/>
        {left !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{left}</Text>}
      </View>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{title}</Text>
      {progress !== undefined && <View style={{height: 3, borderRadius: 2, marginVertical: theme.space.tight, backgroundColor: alpha(theme.colors.text, .14)}}><View style={{width: `${Math.round(progress * 100)}%`, height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View>}
      {next !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{next}</Text>}
    </View>
  </Focusable>;
}

export type AiringRowProps = Readonly<{
  /** The start time, in the row's first column. */
  time: string;
  /** The episode code and name, or the programme. */
  title: string; code?: string;
  /** The time range or the channel, under the title. */
  detail?: string;
  /** A Remind me bell, a Record mark, Series. */
  trailing?: ReactNode;
  onPress?: () => void;
}>;

/** One airing in a list (More airings and Later on in the programme pane, Coming up in a rule, a phone's Upcoming). */
export function AiringRow({time, title, code, detail, trailing, onPress}: AiringRowProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable onPress={onPress} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, minHeight: frame.rowTwoLine - theme.space.sm, borderTopWidth: 1, borderColor: theme.colors.line}}>
    <Text style={[theme.type.rowLabel, {width: guideSpec.timeColumn[frame.form], color: theme.colors.textSecondary}]}>{time}</Text>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{code !== undefined && <Text style={{fontFamily: theme.type.listLabel.fontFamily}}>{code}  </Text>}{title}</Text>
      {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
    {trailing}
  </Focusable>;
}
