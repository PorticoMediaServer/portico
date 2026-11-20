import React, {useState, type ReactElement, type ReactNode, type ComponentProps, type Ref} from 'react';
import {FlatList, Text, View, type ListRenderItemInfo} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {titleTintHue} from '../../../../../packages/client-core/src/presentation/title-page';
import type {RecordingGroups} from '../../../../../packages/client-core/src/dvr';
import type {NativeShellRuntime} from '../../shell/runtime';
import {TVRow} from '../layout/TVRow';
import {Chip, Tabs} from '../controls';
import {Artwork, ChannelMark, GeneratedArt, type ArtworkSource} from '../artwork';
import {Progress} from '../cards/primitives';
import {Button} from '../controls/Button';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {ListEdgeFade} from '../layout/ListEdgeFade';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme, type Theme} from '../theme';
import {useDockClearance} from '../dock/DockClearance';
import {frameSpec} from '@portico/design';
import {Flag, guideSpec, type GuideChannel} from './Guide';

/** A recording's picture beside its words: 16:9 from tablet width; a phone's row has none (phone-app 12). */
const recordingArt = frameSpec.rowArt.recording;

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
  focus?: Pick<ComponentProps<typeof Focusable>, 'focusId' | 'testID' | 'onFocus' | 'onBlur' | 'onLongPressAction'> & {node?: Ref<View>};
  more?: Readonly<{label: string; onPress: () => void}>;
}>;

/** One upcoming or recording-now row (desktop-live live-upcoming, phone-app 12). */
export function RecordingRow({time, range, title, flags, detail, channel, source, artwork, status, onPress, more, focus}: RecordingRowProps) {
  const theme = useTheme(), frame = useFrameMetrics(), art = recordingArt[frame.form];
  const tone = status?.tone === 'record' ? theme.colors.record : status?.tone === 'accent' ? theme.colors.accentStrong : theme.colors.textSecondary;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, paddingVertical: theme.space.xs}}>
    <View style={{width: guideSpec.timeColumn[frame.form] + (art ? theme.space.md : 0)}}>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{time}</Text>
      {range !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{range}</Text>}
    </View>
    <Focusable ref={focus?.node} focusId={focus?.focusId} testID={focus?.testID} onFocus={focus?.onFocus} onBlur={focus?.onBlur}
      onLongPressAction={focus?.onLongPressAction} focusKind="row" accessibilityLabel={title} onPress={onPress} style={{flex: 1, minWidth: 0, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
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
    {more && frame.form !== 'tv' && <Button variant="icon" size="small" icon="i-more" iconOnly testID={focus?.testID && `${focus.testID}-more`} accessibilityLabel={more.label} onPress={more.onPress}/>}
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
  focus?: Pick<ComponentProps<typeof Focusable>, 'focusId' | 'testID' | 'onFocus' | 'onBlur' | 'onLongPressAction'> & {node?: Ref<View>};
  more?: Readonly<{label: string; onPress: () => void}>;
}>;

/** One series rule (desktop-live live-rules): its picture, what it records, what is next, On or Paused. The web kit's `dvr` RuleCard is the same part (it draws a generated picture from `glyph`). */
export function RuleCard({title, summary, next, warning = false, state, upcoming, artwork, selected = false, onPress, more, focus}: RuleCardProps) {
  const theme = useTheme(), frame = useFrameMetrics(), art = recordingArt[frame.form];
  return <Focusable ref={focus?.node} focusId={focus?.focusId} testID={focus?.testID} onFocus={focus?.onFocus} onBlur={focus?.onBlur}
      onLongPressAction={focus?.onLongPressAction} focusKind="row" accessibilityLabel={title} onPress={onPress} style={{marginHorizontal: frame.cardInset, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, padding: frame.cardPadding / 2 + theme.space.tight, borderRadius: frame.cardRadius,
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
    {more && frame.form !== 'tv' && <Button variant="icon" size="small" icon="i-more" iconOnly testID={focus?.testID && `${focus.testID}-more`} accessibilityLabel={more.label} onPress={more.onPress}/>}
  </Focusable>;
}

export type ConflictCardProps = Readonly<{title: string; body: string; children?: ReactNode; action?: Readonly<{label: string; onPress: () => void; focus?: RecordingRowProps['focus']}>}>;

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
    {action && <Button ref={action.focus?.node} focusId={action.focus?.focusId} testID={action.focus?.testID} onFocus={action.focus?.onFocus} onBlur={action.focus?.onBlur}
      onLongPressAction={action.focus?.onLongPressAction} variant="glass" size="small" onPress={action.onPress}>{action.label}</Button>}
  </View>;
}

export function RecordingStorageSummary({title, used, free, remaining, fraction, onPress}: {
  title: string; used: string; free: string; remaining: string; fraction?: number; onPress: () => void;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), dock = useDockClearance();
  return <Focusable accessibilityRole="button" accessibilityLabel={[title, used, free, remaining].join(', ')} onPress={onPress}
    style={{marginHorizontal: frame.cardInset, marginBottom: theme.space.sm + dock, padding: frame.cardPadding, gap: theme.space.xs,
      borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}>
    <View style={{flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', gap: theme.space.sm}}>
      <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{used}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{free}</Text>
      <View style={{flex: 1}}/><Icon name="i-right" size={theme.type.listLabel.fontSize} color={theme.colors.textTertiary}/>
    </View>
    <Progress value={fraction} height={theme.space.tight}/>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{remaining}</Text>
  </Focusable>;
}

export function recordingGridColumns({width, isTV}: Pick<Theme, 'width' | 'isTV'>): number {
  const grid = frameSpec.live.recordedGrid;
  return isTV ? grid.tv : width >= grid.wideMin ? grid.tablet : grid.phone;
}

export function RecordingList<Row>({listRef, columns, lead, fade, renderItem, ...list}: Omit<ComponentProps<typeof FlatList<Row>>, 'renderItem'> & {
  listRef?: Ref<FlatList<Row>>; columns?: number; lead?: ReactNode; fade?: boolean;
  renderItem: (info: ListRenderItemInfo<Row>, cardWidth: number) => ReactElement | null;
}) {
  const theme = useTheme(), gap = theme.space.sm;
  const [width, setWidth] = useState(0);
  const measureGrid = !theme.isTV && columns !== undefined;
  const cardWidth = theme.isTV ? 385 : columns ? Math.max(1, (width - (columns - 1) * gap) / columns) : 0;
  return <View style={{flex: 1}} onLayout={measureGrid ? event => setWidth(event.nativeEvent.layout.width) : undefined}>
    {lead}
    {(!measureGrid || width > 0) && <FlatList {...list} ref={listRef} numColumns={columns} columnWrapperStyle={columns && columns > 1 ? {gap} : undefined}
      renderItem={info => renderItem(info, cardWidth)} contentContainerStyle={{gap, paddingBottom: theme.space.xl}}/>}
    {fade && <ListEdgeFade edge="bottom" length={theme.targets.touch} opacity={0.94}/>}
  </View>;
}

export function RecordingResults({region, children}: {region: FocusRegionProps; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <FocusRegion {...region} style={{flex: 1, paddingHorizontal: theme.isTV ? 0 : frame.gutter}}>{children}</FocusRegion>;
}

export function RecordingSection({title, action}: {title: string; action?: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {flex: 1, color: theme.colors.text}]}>{title}</Text>
    {action}
  </View>;
}

type Group = RecordingGroups['groups'][number];

export function RecordingFacts({items}: {items: readonly (string | undefined)[]}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
    {items.filter(Boolean).map((item, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{item}</Text>)}
  </View>;
}

export function RecordingSpaceFacts({used, free, fraction, remaining, policy, warning}: {
  used: string; free: string; fraction?: number; remaining?: string; policy?: string; warning?: string;
}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.sm}}>
    <RecordingFacts items={[used, free, remaining]}/>
    {fraction !== undefined && <Progress value={fraction} height={theme.space.xs}/>}
    {policy && <RecordingFacts items={[policy]}/>}
    {warning && <Text style={[theme.type.rowLabel, {color: theme.colors.record}]}>{warning}</Text>}
  </View>;
}

export function RecordingViewTabs({region, choices, value, onChange}: {
  region: FocusRegionProps; choices: readonly {value: string; label: string; count?: number}[]; value: string; onChange: (value: string) => void;
}) {
  const theme = useTheme();
  return theme.isTV ? <FocusRegion {...region} style={{flexDirection: 'row', gap: theme.space.sm}}>
    {choices.map(choice => <Chip key={choice.value} focusId={choice.value} testID={`recordings-view-${choice.value}`}
      selected={choice.value === value} onFocus={() => onChange(choice.value)} onPress={() => onChange(choice.value)} count={choice.count}>{choice.label}</Chip>)}
  </FocusRegion> : <TVRow><Tabs value={value} items={choices} onValueChange={onChange}
    itemProps={item => ({testID: `recordings-view-${item.value}`})}/></TVRow>;
}

function ProgrammeArt({title, seed, image, runtime, width}: {title: string; seed: string; image?: string; runtime?: NativeShellRuntime; width: number}) {
  const source = image && runtime?.artwork(image), hue = titleTintHue(seed);
  return <View style={{width, aspectRatio: 16 / 9, backgroundColor: `hsl(${hue}, 32%, 22%)`, borderRadius: 12, overflow: 'hidden'}}><GeneratedArt glyph={(Array.from(title.trim())[0] ?? '').toLocaleUpperCase(defaultI18n.locale)} palette={{first: `hsl(${hue}, 40%, 32%)`, second: `hsl(${hue}, 30%, 12%)`}}/>{source && <Artwork source={source}/>}</View>;
}
export function RecordingCard({value, runtime, width, onOpen, onFocus, onBlur}: {value: Group; width: number; runtime: NativeShellRuntime; onOpen: () => void; onFocus: () => void; onBlur: () => void}) {
  const theme = useTheme(), t = defaultI18n.t, recording = value.recording;
  const status = [value.kept > 0 ? t('recordings.kept') : undefined, value.exceptions > 0 ? t('web.dvr.state.incomplete') : undefined];
  return <Focusable onFocus={onFocus} onBlur={onBlur} focusId={value.key} testID={`recordings-group-${value.key}`} focusKind="card"
    onPress={onOpen} style={{width, gap: theme.space.xs}}>
    <View style={{aspectRatio: 16 / 9, borderRadius: theme.radii.artwork, overflow: 'hidden'}}>
      <ProgrammeArt title={value.title} seed={recording.occurrence.channelId} image={recording.programme.image} runtime={runtime} width={width}/>
      {recording.watched === false && value.recordings === 1 && <View style={{position: 'absolute', right: theme.space.sm, top: theme.space.sm,
        width: theme.space.xs, height: theme.space.xs, borderRadius: theme.space.xs, backgroundColor: theme.colors.accentStrong}}/>}
    </View>
    <Text style={[theme.type.cardTitle, {color: theme.colors.text}]}>{value.title}</Text>
    <RecordingFacts items={[recording.channel?.name, ...status, t(value.unwatched ? 'recordings.showCountUnwatched' : 'recordings.showCount', {count: value.recordings, unwatched: value.unwatched})]}/>
  </Focusable>;
}
