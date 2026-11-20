import React, {useState, type ReactNode} from 'react';
import {ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import type {ArtworkSource} from '../artwork/types';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {SkeletonBlock} from '../states/SkeletonBlock';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {StartFocusScope} from '../focus';
import {useFrameMetrics} from './metrics';
import {PlayerControl} from '../player/parts';
import {AudioChoiceRow} from '../player/queue';
import {ArtBox} from './parts';

/** One control in a fixed place. A control the item doesn't support keeps its place with `disabled`, dimmed. */
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; /** Back or forward: the seconds it skips, drawn in the arrow. */ skip?: number; testID?: string; anchorRef?: React.Ref<View>}>;
/** An audiobook's Speed, Sleep timer and Chapters. A phone draws each as a tile (its `value` over its label, outlined
 * in the accent when `active`); the wide forms draw Speed and Sleep timer as a row of their `choices` with `help`. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string; help?: string; choices?: readonly Readonly<{label: string; selected?: boolean; onPress: () => void}>[]}>;
/** The time read-outs: where it is and, at the other end, the length (music on a wide form) or the time left. */
export type AudioTimes = Readonly<{position: string; end: string; /** A phone's audiobook: the time left in the book, between them. */ middle?: string}>;

export type AudioPlayerFrameProps = Readonly<{
  /** Close (the down arrow), then where this is playing from ("Playing from" / "After Hours"). */
  close: AudioControl; context?: string; source?: string;
  /** The device it plays on ("This browser", "This iPad"): the bottom row's middle on a phone, the pill row on an upright
   * tablet, the top right beside More on the wide forms. Not drawn on TV. */
  device?: AudioControl;
  /** More: the top right (a phone, a tablet, the web); the button row on TV. */
  more: AudioControl;
  /** A note over the top bar for a moment ("Went back 30 s, since it's been a while."). Wide forms. */
  notice?: string;
  /** The cover. With none, the placeholder with `icon` (music or book) fills the same box. */
  artwork?: ArtworkSource | null; icon: IconName;
  /** The screen's own view in the artwork's box (the listening cover the dock's morph paints into); drawn instead of `artwork`. */
  cover?: ReactNode;
  title: string;
  /** "Unabridged", after the title. Wide forms. */
  badge?: string;
  /** The artist then the album; an audiobook's author then "Read by" the narrator. A phone says the first only; TV puts the second on its own line. */
  byline?: readonly string[];
  /** The heart at the end of the title row, and on the wide forms More beside it. Not drawn on TV. */
  favourite?: AudioControl; titleMore?: AudioControl;
  /** An audiobook: the chapter line over the scrubber ("Chapter 8" ... "of 19"). */
  chapter?: Readonly<{label: string; of: string}>;
  /** The music signal line (what the sound passes through), under the title and byline. */
  signal?: ReactNode;
  /** The kit's scrubber line; the frame places `times` for the form: under its ends on a phone and TV, at its ends elsewhere. */
  scrubber: ReactNode; times: AudioTimes;
  /** Five fixed slots; the middle one is Play or Pause. Music's ends are shuffle and repeat (the secondary class). */
  transport: readonly [AudioControl, AudioControl, AudioControl, AudioControl, AudioControl];
  /** A phone only: what plays next ("Next" / "Scared To Live"). Its line is kept, blank, when there is nothing to say. */
  next?: Readonly<{label: string; title: string}>;
  /** Lyrics and Up next: the bottom row's ends on a phone, the pill row on an upright tablet. The panel's tabs on the
   * wide forms; Lyrics is a labelled button on TV, where the queue is always shown. */
  lyrics?: AudioControl; queue?: AudioControl;
  /** TV: Add to playlist, beside Lyrics. */
  playlist?: AudioControl;
  /** An audiobook: Speed, Sleep timer and Chapters (Chapters is the panel's tab on the wide forms). */
  speed?: AudioOption; sleep?: AudioOption; chapters?: AudioOption;
  /** The queue, the one flexible region (`AudioPanel`): a raised panel beside the column on the wide forms, the list
   * after the controls on TV, and on a phone a sheet over the player while it is open. */
  panel?: ReactNode;
  loading?: boolean;
  testID?: string;
  /** TV: the control the page starts on (R1), by its `focusId` or `testID`. Never in the rail. */
  startFocus?: string;
}>;

const spec = frameSpec.audio, wideSpec = spec.wide, icons = spec.icons, tvSpec = spec.tv;

/** Frame 8, now playing for music and audiobooks (rule 12; R7: each form its own layout). Every region has a fixed
 * box, so a missing cover, a long title or a missing control moves nothing. The frame draws no backdrop: the layer's
 * glow from the artwork is the audio player's (`Layer kind="audio"`).
 * - A phone (phone-app 14 and 15) is one column: the bar, the artwork, the title, the scrubber and transport, then
 *   the next line and the bottom row; what is left of the height is shared under the transport and under the next line.
 * - A tablet and the web (desktop-listen "now", tablet-play 9 and 10) put a column as wide as the cover (title with the
 *   heart and More, the scrubber with its times at its ends, a compact transport, the pill row) beside a
 *   raised queue panel, under a bar with Close, the context, the device and More. An audiobook (book-player,
 *   tablet-play 12) has its cover beside its title, the chapter line, a book-long scrubber, and Speed and Sleep timer rows.
 * - TV (final3-tv-12): the cover, then the title column at the mock-up's places, then the queue to the safe edge. */
export function AudioPlayerFrame({startFocus, ...props}: AudioPlayerFrameProps) {
  const {form} = useFrameMetrics();
  return <StartFocusScope id={startFocus}>{form === 'phone' ? <PhoneAudio {...props}/> : form === 'tv' ? <TVAudio {...props}/> : <WideAudio {...props}/>}</StartFocusScope>;
}

/** The five transport slots at the form's icon classes: Play or Pause the white disc, Previous and Next (or the skips)
 * white glyphs, shuffle and repeat smaller and dimmer; a repeat that is on is the accent with a dot under it. */
function Transport({transport, loading, gap}: {transport: AudioPlayerFrameProps['transport']; loading: boolean; gap?: number}) {
  const theme = useTheme(), {form} = useFrameMetrics(), book = transport[1].skip !== undefined;
  const control = (item: AudioControl, index: number) => {
    const end = index === 0 || index === 4, size = index === 2 ? icons.primary[form] : icons.control[form];
    return <View key={item.label} style={{alignItems: 'center'}}>
      <PlayerControl {...item} primary={index === 2} plain={end} size={size} iconSize={index === 2 ? icons.primaryIcon[form] : end && !book ? icons.secondaryIcon[form] : book && end ? icons.stepIcon[form] : item.skip !== undefined ? icons.stepIcon[form] + theme.space.xs : icons.stepIcon[form]}
        tone={end && !book && !item.active ? 'secondary' : undefined} disabled={item.disabled || loading}/>
      {item.active && <View style={{position: 'absolute', bottom: theme.space.hairline, width: theme.space.hairline * 4, height: theme.space.hairline * 4, borderRadius: theme.space.hairline * 2, backgroundColor: theme.colors.accentStrong}}/>}
    </View>;
  };
  return <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: gap === undefined ? 'space-between' : 'center', gap}}>{transport.map(control)}</View>;
}

function Context({context, source, loading, centred}: {context?: string; source?: string; loading: boolean; centred: boolean}) {
  const theme = useTheme();
  // Rule 10: "Playing from" and its source wrap to another line; nothing is cut.
  return <View style={{flex: 1, minWidth: 0, alignItems: centred ? 'center' : 'flex-start', flexDirection: centred ? 'column' : 'row', flexWrap: centred ? undefined : 'wrap', columnGap: theme.space.tight}}>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, textAlign: centred ? 'center' : undefined}]}>{loading ? '' : context}</Text>
    {source !== undefined && !loading && <Text style={[centred ? theme.type.rowLabel : theme.type.rowHelp, {flexShrink: 1, color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily, textAlign: centred ? 'center' : undefined}]}>{source}</Text>}
  </View>;
}

function Art({artwork, cover, icon, title, size, loading}: {artwork?: ArtworkSource | null; cover?: ReactNode; icon: IconName; title: string; size: number; loading: boolean}) {
  const theme = useTheme();
  return <View style={{width: size, height: size, borderRadius: theme.radii.artwork, ...shadow}}>{loading ? <SkeletonBlock width={size} height={size} radius={theme.radii.artwork} shine/> : cover ?? <ArtBox kind="square" source={artwork} icon={icon} width={size} alt={title}/>}</View>;
}
const shadow = {shadowColor: '#000', shadowOpacity: .6, shadowRadius: 30, shadowOffset: {width: 0, height: 24}, elevation: 12} as const;

function Times({times, loading, ends}: {times: AudioTimes; loading: boolean; ends?: ReactNode}) {
  const theme = useTheme(), style = [theme.type.playerTime, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums' as const]}];
  if (ends !== undefined) return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
    <Text style={[style, {minWidth: wideSpec.timesWidth}]}>{loading ? '' : times.position}</Text><View style={{flex: 1}}>{ends}</View><Text style={[style, {minWidth: wideSpec.timesWidth, textAlign: 'right'}]}>{loading ? '' : times.end}</Text>
  </View>;
  return <View style={{flexDirection: 'row', justifyContent: 'space-between', alignItems: 'center', gap: theme.space.xs}}>
    <Text style={style}>{loading ? '' : times.position}</Text>{times.middle !== undefined && <Text numberOfLines={1} style={[style, {flexShrink: 1}]}>{loading ? '' : times.middle}</Text>}<Text style={style}>{loading ? '' : times.end}</Text>
  </View>;
}

function Glyph({item, loading, size, disc = false}: {item: AudioControl; loading: boolean; size?: number; disc?: boolean}) {
  const theme = useTheme();
  return <Button ref={item.anchorRef} testID={item.testID} variant={disc ? 'glass' : 'text'} size="small" iconOnly icon={item.icon} iconSize={size} iconColor={theme.colors.text} accessibilityLabel={item.label} accessibilityState={{selected: item.active}} disabled={item.disabled || loading} onPress={item.onPress}/>;
}

function ChapterLine({chapter, loading, wide}: {chapter: NonNullable<AudioPlayerFrameProps['chapter']>; loading: boolean; wide: boolean}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, minHeight: theme.type.rowLabel.lineHeight}}>
    {!wide && <Icon name="i-list" size={frameSpec.player.pillIcon} color={theme.colors.textSecondary}/>}
    <Text numberOfLines={1} style={[theme.type.rowLabel, {flex: 1, color: wide ? theme.colors.text : theme.colors.textSecondary, fontFamily: wide ? theme.type.groupLabel.fontFamily : theme.type.rowLabel.fontFamily}]}>{loading ? '' : chapter.label}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{loading ? '' : chapter.of}</Text>
  </View>;
}

/** A phone's bottom-row slot: a glyph over its label, the device with its glyph beside its label, or an audiobook's tile. */
function Slot({item, loading, tile = false, inline = false}: {item: AudioControl | AudioOption; loading: boolean; tile?: boolean; inline?: boolean}) {
  const theme = useTheme(), off = item.disabled || loading, value = 'value' in item ? item.value : undefined, tint = item.active ? theme.colors.accentStrong : theme.colors.text;
  const glyph = item.icon && <Icon name={item.icon} size={frameSpec.player.promptIcon} color={tile ? item.active ? theme.colors.accentStrong : theme.colors.textSecondary : tint}/>;
  return <Focusable ref={item.anchorRef} testID={item.testID} accessibilityRole="button" accessibilityLabel={value !== undefined ? `${item.label}, ${value}` : item.label} accessibilityState={{disabled: !!off, selected: item.active}} disabled={off} onPress={item.onPress} focusRadius={theme.baseRadii.control}
    style={[{flex: 1, height: '100%', alignItems: 'center', justifyContent: 'center', gap: theme.space.tight, opacity: off ? theme.spec.button.disabledOpacity : 1}, inline && !tile && {flexDirection: 'row', gap: theme.space.xs},
      tile && {borderRadius: frameSpec.card.radius.phone, paddingHorizontal: theme.space.tight, backgroundColor: item.active ? alpha(theme.colors.accent, .24) : alpha(theme.colors.text, frameSpec.player.tint.pill), borderWidth: theme.space.hairline, borderColor: item.active ? alpha(theme.colors.accentStrong, .7) : 'transparent'}]}>
    {value !== undefined ? <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>{glyph}<Text numberOfLines={1} style={[theme.type.rowLabel, {color: tile && item.active ? theme.colors.accentStrong : theme.colors.text, fontFamily: theme.type.pageTitle.fontFamily}]}>{value}</Text></View> : glyph}
    <Text numberOfLines={2} style={[inline && !tile ? theme.type.rowLabel : theme.type.rowHelp, {textAlign: 'center', color: tile && item.active ? theme.colors.accentStrong : theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{item.label}</Text>
  </Focusable>;
}

function PhoneAudio({close, more, context, source, device, artwork, cover, icon, title, byline, favourite, chapter, signal, scrubber, times, transport, next: upcoming, lyrics, queue, speed, sleep, chapters, panel, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), form = 'phone' as const, inset = spec.inset[form];
  const book = chapter !== undefined, tiles = [speed, sleep, chapters, device].filter((item): item is AudioOption => item !== undefined);
  // The artwork is the spec's size where the window has room; a short window gets the largest square that leaves every fixed region its place.
  const fixed = (book ? frame.rowTwoLine / 2 : 0) + spec.topBar[form] + spec.gap[form] + theme.space.sm + spec.titleSlot[form] + spec.scrubber[form] + spec.transport[form] + spec.nextLine[form] + spec.bottomRow[form] + insets.top + Math.max(insets.bottom, frameSpec.sheet.footerClear);
  const column = Math.max(0, Math.min(spec.artwork[form], frame.width - 2 * inset, window.height - fixed));
  const words = <View style={{height: spec.titleSlot[form], flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.xs}}>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>{loading ? <><SkeletonBlock width="64%" height={theme.type.title.fontSize}/><SkeletonBlock width="40%" height={theme.type.intro.fontSize}/></> : <>
      {/* The title takes the lines it needs; the slot keeps two, and a longer title runs into the room under it rather than being cut. */}
      <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      <Text numberOfLines={1} style={[theme.type.intro, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily}]}>{byline?.[0] ?? ' '}</Text>
    </>}</View>
    {favourite && !loading && <Glyph item={favourite} loading={loading} size={icons.inlineIcon[form]}/>}
  </View>;
  const nextLine = <View style={{height: spec.nextLine[form], justifyContent: 'center'}}>{upcoming && !loading && <Text numberOfLines={1} style={[theme.type.rowHelp, {textAlign: 'center', color: theme.colors.textTertiary}]}>
    {upcoming.label}  <Text style={{color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}}>{upcoming.title}</Text></Text>}</View>;
  const bottom = <View style={{height: spec.bottomRow[form], flexDirection: 'row', alignItems: 'center', gap: book ? theme.space.xs : 0}}>
    {book ? tiles.map(item => <Slot key={item.label} item={item} loading={loading} tile/>)
      : <>{lyrics && <Slot item={lyrics} loading={loading}/>}{device && <View style={{flex: 1.6, height: '100%'}}><Slot item={device} loading={loading} inline/></View>}{queue && <Slot item={queue} loading={loading}/>}</>}
  </View>;
  return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>
    <View style={{flex: 1, paddingHorizontal: inset}}>
      <View style={{height: spec.topBar[form], flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}><Glyph item={close} loading={false}/><Context context={context} source={source} loading={loading} centred/><Glyph item={more} loading={loading}/></View>
      <View style={{marginTop: spec.gap[form], alignItems: 'center'}}><Art artwork={artwork} cover={cover} icon={icon} title={title} size={book ? column * spec.bookArtShare : column} loading={loading}/></View>
      <View style={{marginTop: theme.space.sm}}>{words}</View>
      {signal !== undefined && !loading && <View style={{alignItems: 'center', marginTop: theme.space.xs}}>{signal}</View>}
      {chapter && <ChapterLine chapter={chapter} loading={loading} wide={false}/>}
      <View style={{height: spec.scrubber[form], justifyContent: 'center', gap: frameSpec.player.timesGap[form]}}>{scrubber}<Times times={times} loading={loading}/></View>
      <View style={{height: spec.transport[form], justifyContent: 'center'}}><Transport transport={transport} loading={loading}/></View>
      {/* What is left of the height is shared under the transport and under the next line, the same on every phone. */}
      <View style={{flex: 1}}/>{nextLine}<View style={{flex: 1}}/>
      {bottom}
    </View>
    {panel !== undefined && <View style={[StyleSheet.absoluteFill, {top: frameSpec.sheet.fullTop + insets.top, borderTopLeftRadius: frameSpec.sheet.fullRadius, borderTopRightRadius: frameSpec.sheet.fullRadius, overflow: 'hidden', backgroundColor: theme.colors.raised}]}>{panel}</View>}
  </View>;
}

function WideAudio({close, more, context, source, device, notice, artwork, cover, icon, title, badge, byline = [], favourite, titleMore, chapter, signal, scrubber, times, transport, lyrics, queue, speed, sleep, panel, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), window = useWindowDimensions(), form = frame.form === 'medium' ? 'medium' as const : 'expanded' as const;
  // An upright tablet (tablet-play 10) keeps the device with Lyrics and Up next in a pill row under the transport; a tablet on its side and the web put it at the top right.
  const upright = window.height > window.width, book = chapter !== undefined, edge = wideSpec.panelInset[form], panelWidth = panel !== undefined ? spec.upNextWidth[form] : 0;
  const top = <View style={{height: spec.topBar[form], flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: edge}}>
    <Glyph item={close} loading={false} disc/><Context context={context} source={source} loading={loading} centred={false}/>
    {device && !upright && <Button testID={device.testID} variant="glass" size="small" icon={device.icon} disabled={device.disabled || loading} onPress={device.onPress}>{device.label}</Button>}
    <Glyph item={more} loading={loading} disc/>
  </View>;
  // The column is as wide as the cover, which is the spec's size or the largest that leaves the column's fixed rows their room.
  const rows = spec.titleSlot[form] + wideSpec.scrubberGap[form] + spec.scrubber[form] + wideSpec.transportGap[form] + icons.primary[form] + wideSpec.belowGap[form] + theme.targets.touch;
  const room = window.height - insets.top - insets.bottom - spec.topBar[form] - wideSpec.titleGap[form] - rows - theme.space.lg;
  const column = book ? wideSpec.bookColumn[form] : Math.max(0, Math.min(spec.artwork[form], frame.width - panelWidth - 3 * edge, room));
  // R2: the title is the form scale's `audioTitle`, a song's and an audiobook's alike.
  const titleSize = theme.type.audioTitle.fontSize;
  const words = (size: 'title' | 'pageTitle') => <View style={{flex: 1, minWidth: 0, gap: theme.space.xs}}>{loading ? <><SkeletonBlock width="64%" height={titleSize}/><SkeletonBlock width="40%" height={theme.type.intro.fontSize}/></> : <>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.sm, rowGap: theme.space.xs}}>
      <Text accessibilityRole="header" style={[theme.type[size], theme.type.audioTitle, {color: theme.colors.text, includeFontPadding: false}]}>{title}</Text>
      {badge !== undefined && <View style={{paddingHorizontal: theme.space.xs, paddingVertical: theme.space.hairline * 2, borderRadius: theme.baseRadii.control, backgroundColor: alpha(theme.colors.text, frameSpec.player.tint.pill)}}><Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{badge}</Text></View>}
    </View>
    {/* The album after the artist (desktop-listen "now"); "Read by" the narrator after the author. */}
    <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{byline.join('   ')}</Text>
  </>}</View>;
  const marks = !book && !loading && (favourite || titleMore) && <View style={{flexDirection: 'row', alignSelf: 'flex-end'}}>{favourite && <Glyph item={favourite} loading={loading} size={icons.inlineIcon[form]}/>}{titleMore && <Glyph item={titleMore} loading={loading} size={icons.inlineIcon[form]}/>}</View>;
  const head = book ? <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: wideSpec.bookArtGap}}><Art artwork={artwork} cover={cover} icon={icon} title={title} size={wideSpec.bookArt[form]} loading={loading}/>{words('pageTitle')}</View>
    : <><Art artwork={artwork} cover={cover} icon={icon} title={title} size={column} loading={loading}/><View style={{marginTop: wideSpec.titleGap[form], flexDirection: 'row', alignItems: 'flex-end', gap: theme.space.sm}}>{words('pageTitle')}{marks}</View></>;
  const pills = upright && !book && [lyrics, device, queue].some(Boolean) && <View style={{flexDirection: 'row', justifyContent: 'center', gap: theme.space.controlGap}}>
    {[lyrics, device, queue].filter((item): item is AudioControl => item !== undefined).map(item => <Button key={item.label} testID={item.testID} variant="glass" size="small" icon={item.icon} disabled={item.disabled || loading} onPress={item.onPress}
      style={{borderRadius: theme.targets.touch / 2}}>{item.label}</Button>)}</View>;
  const options = book && [speed, sleep].some(item => item?.choices) && <View style={{gap: theme.space.controlGap}}>
    {[speed, sleep].filter((item): item is AudioOption => !!item?.choices).map(item => <AudioChoiceRow key={item.label} testID={item.testID ?? item.label} label={item.label} help={item.help} choices={item.choices!}/>)}</View>;
  const main = <View style={{width: column, alignSelf: 'center'}}>
    {head}
    {signal !== undefined && !loading && <View style={{marginTop: theme.space.xs}}>{signal}</View>}
    {chapter && <View style={{marginTop: wideSpec.chapterGap}}><ChapterLine chapter={chapter} loading={loading} wide/></View>}
    <View style={{marginTop: chapter ? theme.space.xs : wideSpec.scrubberGap[form]}}><Times times={times} loading={loading} ends={scrubber}/></View>
    <View style={{marginTop: wideSpec.transportGap[form]}}><Transport transport={transport} loading={loading} gap={wideSpec.transportSpacing[form]}/></View>
    {(pills || options) && <View style={{marginTop: book ? wideSpec.rowsGap : wideSpec.belowGap[form], gap: wideSpec.belowGap[form]}}>{options}{pills}</View>}
  </View>;
  return <View testID={testID} style={{flex: 1, paddingTop: insets.top, paddingBottom: Math.max(insets.bottom, edge)}}>
    {top}
    {notice !== undefined && !loading && <View pointerEvents="none" style={{position: 'absolute', top: insets.top + (spec.topBar[form] - theme.targets.touch) / 2, left: 0, right: 0, alignItems: 'center'}}>
      <View style={{height: theme.targets.touch - theme.space.xs, flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: theme.space.sm, borderRadius: theme.targets.touch, backgroundColor: alpha(theme.colors.ink, .55), borderWidth: theme.space.hairline, borderColor: alpha(theme.colors.text, .1)}}>
        <Icon name="i-rew" size={frameSpec.player.pillIcon} color={theme.colors.text}/><Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{notice}</Text></View></View>}
    <View style={{flex: 1, flexDirection: 'row', gap: edge, paddingHorizontal: edge}}>
      <ScrollView style={{flex: 1}} contentContainerStyle={{flexGrow: 1, justifyContent: 'center', paddingVertical: theme.space.sm}}>{main}</ScrollView>
      {panel !== undefined && <View style={{width: panelWidth, marginTop: theme.space.xs}}>{panel}</View>}
    </View>
  </View>;
}

/** TV (final3-tv-12): the regions at the mock-up's places from the cover's top edge, so nothing moves with the title's length. */
function TVAudio({more, context, source, artwork, cover, icon, title, byline = [], chapter, signal, scrubber, times, transport, lyrics, playlist, speed, sleep, chapters, panel, loading = false, testID}: AudioPlayerFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics(), form = 'tv' as const, art = spec.artwork[form];
  // The title that needed a second line at its role (state keyed by the title, so a new title measures afresh).
  const titleStyle = theme.type.audioTitle, [longTitle, setLongTitle] = useState<string>(), long = longTitle === title;
  const label = (item: AudioControl | AudioOption) => 'value' in item && item.value !== undefined ? `${item.value}  ${item.label}` : item.label;
  const buttons = [...(chapter ? [speed, sleep, chapters] : [lyrics, playlist])].filter((item): item is AudioControl => item !== undefined);
  return <View testID={testID} style={{flex: 1, paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
    <View style={{position: 'absolute', top: tvSpec.fromTop, left: frame.gutter, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <Icon name={icon} size={frameSpec.player.promptIcon + theme.space.tight} color={theme.colors.textSecondary}/>
      <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{loading ? '' : context}</Text>{source !== undefined && !loading && <Text style={[theme.type.intro, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{source}</Text>}
    </View>
    <View style={{position: 'absolute', top: tvSpec.artTop, left: frame.gutter, right: frame.rightGutter, bottom: 0, flexDirection: 'row', gap: spec.gap[form]}}>
      <Art artwork={artwork} cover={cover} icon={icon} title={title} size={art} loading={loading}/>
      <View style={{width: spec.controlsWidth[form]}}>
        <View style={{position: 'absolute', top: tvSpec.titleTop, left: 0, right: 0, height: tvSpec.scrubberTop - tvSpec.titleTop - theme.space.md}}>{loading ? <SkeletonBlock width="70%" height={titleStyle.fontSize}/> : <>
          {/* final3-tv-12: the title is one line at its role, with the artist and album under it, all above the scrubber.
              A title that needs a second line takes the page title's role instead, so it is never cut, never squeezed
              (Android keeps the full line height when it shrinks text) and never pushes the album under the scrubber. */}
          <Text accessibilityRole="header" onTextLayout={event => {if (!long && event.nativeEvent.lines.length > 1) setLongTitle(title);}}
            style={[long ? theme.type.pageTitle : titleStyle, {color: theme.colors.text}]}>{title}</Text>
          {byline[0] !== undefined && <Text numberOfLines={1} style={[theme.type.audioByline, {marginTop: theme.space.sm}]}>{byline[0]}</Text>}
          {byline[1] !== undefined && <Text numberOfLines={1} style={[theme.type.meta, {marginTop: theme.space.xs, color: theme.colors.textTertiary}]}>{byline[1]}</Text>}
          {signal !== undefined && <View style={{marginTop: theme.space.sm}}>{signal}</View>}
        </>}</View>
        <View style={{position: 'absolute', top: tvSpec.scrubberTop - (chapter ? theme.space.xl : 0), width: tvSpec.scrubberWidth, gap: theme.space.sm}}>{chapter && <ChapterLine chapter={chapter} loading={loading} wide/>}{scrubber}<Times times={times} loading={loading}/></View>
        <View style={{position: 'absolute', top: tvSpec.transportTop, width: tvSpec.scrubberWidth}}><Transport transport={transport} loading={loading}/></View>
        <View style={{position: 'absolute', top: tvSpec.moreTop, flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm}}>
          {buttons.map(item => <Button key={item.label} testID={item.testID} variant="glass" size="compact" icon={item.icon} disabled={item.disabled || loading} onPress={item.onPress}>{label(item)}</Button>)}
          <Button testID={more.testID} variant="glass" size="compact" iconOnly icon={more.icon} accessibilityLabel={more.label} disabled={more.disabled || loading} onPress={more.onPress}/>
        </View>
      </View>
      {/* The queue starts where the mock-up's does and runs to the safe edge; with nothing queued the place stays empty. */}
      <View style={{flex: 1, minWidth: spec.upNextWidth[form]}}>{panel}</View>
    </View>
  </View>;
}
