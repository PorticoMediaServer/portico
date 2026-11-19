import React, {useId, useRef, useState, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Artwork, type ArtworkSource} from '../artwork';
import {CircleCard, CollectionBanner, CollectionTile, EpisodeTile, type CollectionCardProps, type EpisodeTileProps} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {Button, Chip, Focusable, KeyHints, Segmented} from '../controls';
import {FocusRegion, FocusWithin} from '../focus';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {TVRow} from '../layout/TVRow';
import {Row, SeeAllLink} from '../layout/Row';
import {HeroMetadata} from '../layout/Hero';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {ListEdgeFade, useListEdges} from '../layout/ListEdgeFade';
import {fontFamilies, useTheme} from '../theme';
import {frameSpec, tvFocusRoom} from '@portico/design';
import {useFrameMetrics} from './metrics';
import {SheetFrame} from './SheetFrame';
import type {TopBarBack} from '../layout/TopBar';

const sizes = frameSpec.hero.title.parts;

/** R5 interim: the 12 between a table's rows and a row's padding (desktop-detail f3), until the rules hand names it. */
const rowPad = 12;

/** The parts a title page's tab panel and side are built from (desktop-detail f1 to f8, phone-detail d1 to d3,
 * desktop-listen, detail-tv-4 to 7), with the web kit's names (web/src/kit/frames/title-parts.tsx). FRAMES.md lists them. */

/** A block of the panel or the side that is not a row of cards, at the page's gutter. A row of cards is the kit `Row`,
 * which places itself. `inset={false}` inside the side, whose column the frame has already placed. */
export function PanelBlock({children, inset = true}: Readonly<{children: ReactNode; inset?: boolean}>) {
  const frame = useFrameMetrics();
  return <View style={{marginLeft: inset ? frame.gutter : 0, marginRight: inset ? frame.rightGutter : 0, minWidth: 0}}>{children}</View>;
}

/** A block's heading (rule 6 row heading), its count and grey note, and its control at the end. */
/** `trailing` sits at the row's end: controls, or words drawn muted (phone-detail d3 frame 7's "6 titles on this server"). */
export function PanelHeading({title, count, note, trailing, quiet = false}: Readonly<{title: string; count?: string; note?: string; trailing?: ReactNode;
  /** A small, muted heading for a list's own count (phone-detail d3 frame 8's "4 films" beside the sort). */
  quiet?: boolean}>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.sm, rowGap: theme.space.controlGap, marginBottom: frame.headingGap}}>
    <Text accessibilityRole="header" style={quiet ? [theme.type.rowHelp, {fontFamily: fontFamilies[600], color: theme.colors.textTertiary}] : [theme.type.rowHeading, {color: theme.colors.text}]}>{title}{count !== undefined ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{'  '}{count}</Text> : null}</Text>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
    {trailing && <View style={{marginLeft: 'auto', flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.controlGap}}>
      {typeof trailing === 'string' ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{trailing}</Text> : trailing}</View>}
  </View>;
}

/** One choice of a few, as a row of chips under a heading (a person's All / Cast / Crew, phone-detail d3 frame 7); the
 * selected one is drawn selected. A leading action chip (Select, on a tablet) may come first. */
export type ChoiceChip = Readonly<{id: string; label: string; selected?: boolean; icon?: IconName; onPress: () => void; testID?: string}>;
export function ChoiceChips({items}: Readonly<{items: readonly ChoiceChip[]}>) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
    {items.map(item => <Chip key={item.id} testID={item.testID} icon={item.icon} selected={item.selected} onPress={item.onPress}>{item.label}</Chip>)}
  </View>;
}

/** A value in the About table; one with `onPress` opens its page (a genre, a person), underlined as drawn. */
export type AboutValue = Readonly<{text: string; onPress?: () => void}>;
/** The About facts (desktop-detail f3, f8; phone-detail d1 frame 2; detail-tv-4; desktop-listen): a label and a value per
 * row. On a phone the table sits on a raised card with hairlines, as drawn. */
export function AboutTable({rows}: Readonly<{rows: readonly Readonly<{label: string; values: readonly AboutValue[]}>[]}>) {
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone', text = phone ? theme.type.about : theme.type.rowHelp;
  // A phone's card sits at the card inset (16), not the page gutter its block starts at, with its text at the card's padding.
  return <View style={phone ? {borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised, marginHorizontal: frame.cardInset - frame.gutter, paddingHorizontal: frame.cardPadding} : {gap: rowPad}}>
    {rows.map((row, index) => <View key={row.label} style={{flexDirection: 'row', gap: phone ? sizes.aboutPhone.gap : theme.space.sm, paddingVertical: phone ? sizes.aboutPhone.pad : 0, borderTopWidth: phone && index > 0 ? 1 : 0, borderTopColor: theme.colors.line}}>
      <Text style={[text, {color: theme.colors.textTertiary, width: phone ? sizes.aboutPhone.label : sizes.aboutLabel[frame.form]}]}>{row.label}</Text>
      <Text style={[text, {flex: 1, color: theme.colors.text}]}>{row.values.map((value, position) => <Text key={position}>{position > 0 ? ', ' : ''}
        {/* TV: plain words, as detail-tv-4 draws them; a link inside a line of text cannot take the remote's focus. */}
        {value.onPress && !theme.isTV ? <Text accessibilityRole="link" onPress={value.onPress} style={{textDecorationLine: 'underline', textDecorationColor: alpha(theme.colors.text, .4), color: phone ? theme.colors.accentStrong : theme.colors.text}}>{value.text}</Text> : value.text}</Text>)}</Text>
    </View>)}
  </View>;
}

export type CastPerson = Readonly<{id: string; name: string; role?: string; portrait?: ArtworkSource; onPress?: () => void}>;
const initials = (name: string) => name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join('');
/** Cast and crew as circles with the name and role under each (desktop-detail f3, phone-detail d1 frame 2, foldable f2,
 * detail-tv-4): initials when there is no photo. A row that scrolls sideways on a phone and TV; the cast, then the crew
 * under a rule, wrapping, on a tablet. */
/** A person with no page of their own: the card is drawn disabled and its navigate does nothing. */
const noPage = () => {};
/** `wrap`: everyone in wrapping rows on every form (the full list in `PeopleSheet`), never one sideways row. */
/** `onEnd`: a TV row that pages, as the old TV title screen's did: called when its last person takes focus, to read the next page. */
export function CastRow({groups, wrap = false, onEnd}: Readonly<{groups: readonly (readonly CastPerson[])[]; wrap?: boolean; onEnd?: () => void}>) {
  const theme = useTheme(), frame = useFrameMetrics(), scroll = !wrap && (frame.form === 'phone' || frame.form === 'tv');
  const size = sizes.castCircle[frame.form], width = size + (frame.form === 'tv' ? 50 : frame.form === 'phone' ? 14 : 24);
  // The kit's person card: the ring wraps only the circle and the whole person grows on focus (detail-tv-4); the name and
  // role take the column's width under the circle.
  // A phone sets the name and the role small, as drawn (phone-detail d1 frame 2 `.pp`).
  const phone = frame.form === 'phone', names = phone ? {titleTextStyle: theme.type.castName, captionTextStyle: theme.type.castRole} : {};
  const lastId = groups.flat().at(-1)?.id;
  const person = (entry: CastPerson) => <CircleCard key={entry.id} {...names} focusId={`cast-${entry.id}`} onFocus={onEnd && entry.id === lastId ? onEnd : undefined} person width={width} circle={size} title={entry.name} titleLines={2} captionLines={2}
    caption={entry.role !== undefined ? [{id: 'role', text: entry.role, role: 'context'}] : []} art={{initials: initials(entry.name), source: entry.portrait, covers: []}}
    onNavigate={entry.onPress ?? noPage} disabled={!entry.onPress}/>;
  const lists = groups.filter(group => group.length > 0);
  // R1, TV: the scroller gives a focused person its growth, ring and glow (`tvFocusRoom`) and takes it back, so the row
  // does not move. Height: the circle; the glow's room covers the name's share of the growth.
  const room = frame.form === 'tv' ? tvFocusRoom(width, size, 'card') : undefined;
  // A phone's row runs to the screen's edges, out of its block's gutter, so the last person shown is cut by the screen (phone-detail d1).
  if (scroll) return <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} style={[room && {marginTop: -room.top, marginBottom: -room.bottom, marginHorizontal: -room.side}, phone && {marginLeft: -frame.gutter, marginRight: -frame.rightGutter}]}
    contentContainerStyle={[{gap: rowPad, alignItems: 'flex-start'}, room && {paddingTop: room.top, paddingBottom: room.bottom, paddingHorizontal: room.side}, phone && {paddingLeft: frame.gutter, paddingRight: frame.rightGutter}]}>{lists.flat().map(person)}</ScrollView></TVRow>;
  return <View style={{gap: theme.space.md}}>{lists.map((group, index) => <View key={index} style={{flexDirection: 'row', flexWrap: 'wrap', rowGap: theme.space.md, columnGap: theme.space.sm,
    paddingTop: index > 0 ? theme.space.md : 0, borderTopWidth: index > 0 ? 1 : 0, borderTopColor: theme.colors.line}}>{group.map(person)}</View>)}</View>;
}

/** The audio and subtitles for the next play, one quiet row under the synopsis on a phone and tablet (phone-detail d1
 * frame 1): "English 7.1 · Automatic", opening one sheet. */
export function TrackPicker({audio, subtitles, label, onPress}: Readonly<{audio: string; subtitles: string; label: string; onPress: () => void}>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Focusable accessibilityRole="button" accessibilityLabel={`${label}: ${audio}, ${subtitles}`} onPress={onPress} focusRadius={frame.cardRadius}
    style={{minHeight: 40, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingHorizontal: rowPad, borderRadius: frame.cardRadius, borderWidth: 1, borderColor: theme.colors.line}}>
    <Icon name="i-vol" size={16} color={theme.colors.textSecondary}/><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{audio}</Text>
    <Icon name="i-subs" size={16} color={theme.colors.textSecondary}/><Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}>{subtitles}</Text>
    <Icon name="i-updown" size={16} color={theme.colors.textTertiary}/>
  </Focusable>;
}

/** One numbered row of a list in order (desktop-detail f8 "Films in order", phone-detail d3 frame 8, detail-tv-7). */
export function NumberedRow({number, artwork, title, facts, synopsis, status, action, progress, onPress}: Readonly<{
  number: number; artwork?: ArtworkSource; title: string; facts?: ReactNode; synopsis?: string;
  status?: Readonly<{label: string; icon?: IconName; accent?: boolean}>; action?: ReactNode; progress?: number; onPress?: () => void;
}>) {
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone', art = sizes.numberedArt[frame.form];
  return <Focusable focusKind="row" focusRadius={frame.cardRadius} accessibilityRole="button" accessibilityLabel={title} onPress={onPress} disabled={!onPress} focusableWhenDisabled={false}
    style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingVertical: rowPad}}>
    {!phone && <Text style={[theme.type.pageTitle, {width: sizes.number, textAlign: 'center', color: alpha(theme.colors.text, .3)}]}>{number}</Text>}
    <View style={{width: art, height: art * 1.5, borderRadius: 6, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      <Artwork source={artwork} width={art} height={art * 1.5}/>
      {progress !== undefined && <View style={{position: 'absolute', left: 0, bottom: 0, height: 3, width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/>}
    </View>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>
      <Text style={[phone ? theme.type.rowLabel : theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      {facts}
      {synopsis !== undefined && !phone && <Text numberOfLines={2} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{synopsis}</Text>}
      {phone && status && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>{status.icon && <Icon name={status.icon} size={14} color={status.accent ? theme.colors.accentStrong : theme.colors.textSecondary}/>}
        <Text style={[theme.type.rowHelp, {color: status.accent ? theme.colors.accentStrong : theme.colors.textSecondary}]}>{status.label}</Text></View>}
    </View>
    {!phone && (status || action) && <View style={{alignItems: 'flex-end', gap: theme.space.controlGap}}>
      {status && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>{status.icon && <Icon name={status.icon} size={14} color={status.accent ? theme.colors.accentStrong : theme.colors.textSecondary}/>}
        <Text style={[theme.type.rowHelp, {color: status.accent ? theme.colors.accentStrong : theme.colors.textSecondary}]}>{status.label}</Text></View>}
      {action}
    </View>}
  </Focusable>;
}

/** The owner's card in the side (desktop-detail f8 "This collection", desktop-listen "Files"). */
export function OwnerCard({title, mark, body, actions = []}: Readonly<{title: string; mark: string; body: string; actions?: readonly Readonly<{icon: IconName; label: string; onPress: () => void}>[]}>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{gap: theme.space.controlGap, padding: frame.cardPadding, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: theme.colors.line}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 999, borderWidth: 1, borderColor: theme.colors.line}]}>{mark}</Text>
    </View>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{body}</Text>
    {actions.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm}}>{actions.map(action => <Button key={action.label} variant="text" size="small" icon={action.icon} onPress={action.onPress}>{action.label}</Button>)}</View>}
  </View>;
}

/** Previous or Next episode beside an episode opened in place (desktop-detail f5, phone-detail d2 frame 4). */
export function EpisodeNeighbour({direction, label, title, detail, artwork, onPress}: Readonly<{direction: 'previous' | 'next'; label: string; title: string; detail?: string; artwork?: ArtworkSource; onPress: () => void}>) {
  const theme = useTheme(), next = direction === 'next', card = sizes.neighbour;
  // Without a still (phone-detail d2 frame 4): a small raised card, its chevron at the outer side, Next set to the right.
  // The frame cuts a long title; here it wraps and the card grows, its neighbour growing with it (titles are never cut).
  if (!artwork) return <Focusable focusKind="row" focusRadius={card.radius} accessibilityRole="button" accessibilityLabel={`${label}: ${title}`} onPress={onPress}
    style={{flexGrow: 1, minHeight: card.height, paddingVertical: theme.space.tight, borderRadius: card.radius, backgroundColor: theme.colors.raisedHigh, flexDirection: next ? 'row-reverse' : 'row', alignItems: 'center', gap: card.gap, paddingLeft: next ? theme.space.controlGap : theme.space.tight, paddingRight: next ? theme.space.tight : theme.space.controlGap}}>
    <Icon name={next ? 'i-right' : 'i-left'} size={card.chevron} color={theme.colors.textTertiary}/>
    <View style={{flex: 1, minWidth: 0, alignItems: next ? 'flex-end' : 'flex-start'}}>
      <Text style={[theme.type.badge, {color: theme.colors.textTertiary}]}>{label}</Text>
      <Text style={[theme.type.cardTitle, {fontFamily: fontFamilies[700], color: theme.colors.text, textAlign: next ? 'right' : 'left'}]}>{title}</Text>
    </View>
  </Focusable>;
  return <Focusable focusKind="row" accessibilityRole="button" accessibilityLabel={`${label}: ${title}`} onPress={onPress} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, padding: 8, borderRadius: 12}}>
    <View style={{width: sizes.neighbourArt, height: sizes.neighbourArt * 9 / 16, borderRadius: 8, overflow: 'hidden', backgroundColor: theme.colors.raised}}><Artwork source={artwork} width={sizes.neighbourArt} height={sizes.neighbourArt * 9 / 16}/></View>
    <View style={{flex: 1, minWidth: 0, gap: 2}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 2}}>{direction === 'previous' && <Icon name="i-left" size={14} color={theme.colors.textTertiary}/>}
        <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{label}</Text>{direction === 'next' && <Icon name="i-right" size={14} color={theme.colors.textTertiary}/>}</View>
      <Text style={[theme.type.cardTitle, {color: theme.colors.text}]}>{title}</Text>
      {detail !== undefined && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
  </Focusable>;
}

/** One row of the TV show page's season column (detail-tv-5a, 5b): the season's name, then how much of it is watched
 * (a short bar and "2 of 13 watched") or its episode count. The season shown in the band beside it is current: the
 * raised fill and the accent bar at its edge (R1 "current"), kept while focus is elsewhere. The real screen's
 * `TVEpisodeBrowser` draws its seasons and its groups of 100 with it. */
export function SeasonListRow({label, detail, progress, current, focusId, testID, onFocus, onPress, onLongPress}: Readonly<{
  label: string; detail?: string;
  /** The watched part (0 to 1), drawn as the short bar before `detail`. */
  progress?: number;
  current: boolean; focusId: string; testID?: string; onFocus?: () => void; onPress: () => void; onLongPress?: () => void}>) {
  const theme = useTheme(), track = theme.spec.card.progress.height[theme.column];
  return <Focusable focusId={focusId} testID={testID} focusKind="row" focusRadius={theme.baseRadii.overlay} accessibilityRole="button" accessibilityState={{selected: current}}
    onFocus={onFocus} onPress={onPress} onLongPress={onLongPress} onLongPressAction={onLongPress}
    style={{minHeight: rowHeight, paddingHorizontal: theme.space.md, paddingVertical: theme.space.controlGap, borderRadius: theme.baseRadii.overlay, justifyContent: 'center', overflow: 'hidden', backgroundColor: current ? alpha(theme.colors.accent, .16) : 'transparent'}}>
    {current && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: theme.space.controlGap, bottom: theme.space.controlGap, width: 3, borderTopRightRadius: 3, borderBottomRightRadius: 3, backgroundColor: theme.colors.accent}}/>}
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{label}</Text>
    {detail !== undefined && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, marginTop: theme.space.tight / 2}}>
      {progress !== undefined && <View style={{width: 72, height: track, borderRadius: track, backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
        <View style={{height: track, width: `${Math.min(1, progress) * 100}%`, borderRadius: track, backgroundColor: theme.colors.accentStrong}}/></View>}
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>
    </View>}
  </Focusable>;
}
/** R5 interim: detail-tv-5a's season rows, 84 high and 6 apart; the column is 330 wide, 70 from the episodes. */
const rowHeight = 84;
/** `window`: rows shown before the list scrolls (six and half the seventh, detail-tv-5e); `fade`: its edge fades; `tile`: the
 * episode still's width (detail-tv-5a, 384); the band's words and credits share the row 640 : 516, its bar is 220. */
export const seasonColumn = {width: 330, gap: 70, rowGap: 6, window: 6.5, fade: 70, fadeOpacity: .9, tile: 384, bandWords: 640, bandCredits: 516, bandBar: 220,
  /** The episode tile the row has moved past: all but hidden, never to nothing (tvOS will not focus a transparent view). */
  passed: .03} as const;

/** "Play a different version" (detail-phone 9b, desktop-detail 9b, detail-tv-8b): one row of two lines per version, in
 * File info's order: the edition or the picture summary, then the rest. The whole row is the button and plays that
 * version at once. On a phone the rows sit on one raised card with hairlines; on TV each is a full-width row. */
export function VersionRows({rows, onChoose}: Readonly<{rows: readonly Readonly<{id: string; heading: string; detail: string}>[]; onChoose: (id: string) => void}>) {
  // The sheet places its own column; the rows add no gutter of the page's.
  const theme = useTheme(), frame = useFrameMetrics(), card = !theme.isTV;
  return <View style={card ? {borderRadius: frame.cardRadius, backgroundColor: theme.colors.raisedHigh, overflow: 'hidden'} : undefined}>
    {rows.map((row, index) => <Focusable key={row.id} focusId={`version-${row.id}`} focusKind="row" focusRadius={card ? 0 : frame.cardRadius} accessibilityRole="button" onPress={() => onChoose(row.id)}
      style={{paddingHorizontal: theme.space.sm, paddingVertical: rowPad, borderTopWidth: card && index > 0 ? 1 : 0, borderTopColor: theme.colors.line, borderRadius: card ? 0 : frame.cardRadius}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{row.heading}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{row.detail}</Text>
    </Focusable>)}
  </View>;
}

/** The owner's Episode display (desktop-detail f4): how a show's episodes are grouped, numbered and ordered, for everyone
 * on the server. Its heading and the "For everyone" mark, a line on what it does, one choice per row, and how an
 * episode will read. It commits as you choose (R9 `choice`). */
export function EpisodeDisplay({title, mark, intro, rows, preview}: Readonly<{title: string; mark: string; intro: string;
  rows: readonly Readonly<{label: string; items: readonly Readonly<{value: string; label: string}>[]; value: string; onValueChange: (value: string) => void}>[];
  /** "Reads as" and the episode as it will read ("S1 E3", "Long, Long Time"). */
  preview: Readonly<{label: string; code: string; title: string}>}>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{gap: theme.space.sm, padding: frame.cardPadding}}>
    <View style={{gap: theme.space.tight}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
        <Text accessibilityRole="header" style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text}]}>{title}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, paddingHorizontal: 8, paddingVertical: 2, borderRadius: 999, borderWidth: 1, borderColor: theme.colors.line}]}>{mark}</Text>
      </View>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{intro}</Text>
    </View>
    {rows.map((row, index) => <View key={row.label} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <Text style={[theme.type.rowHelp, {width: sizes.choiceLabel, color: theme.colors.text, fontFamily: theme.type.rowLabel.fontFamily}]}>{row.label}</Text>
      {/* Beside the label at their own width, so a popover measures the part at its natural size; where the popover is
          narrower (a phone) the choices shrink and their labels take a second line, never cut. */}
      <View style={{flexShrink: 1, minWidth: sizes.choiceMin}}><Segmented testID={`episode-display-${index}`} accessibilityLabel={row.label} items={row.items} value={row.value} onValueChange={row.onValueChange}/></View>
    </View>)}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, padding: rowPad, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{preview.label}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.text, fontFamily: theme.type.bodyStrong.fontFamily}]}>{preview.code}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.text}]}>{preview.title}</Text>
    </View>
  </View>;
}

/** One credits line under the cast on a phone and an upright tablet: "Directed by" and the names (phone-detail d1 frame 2). */
export type TitleCreditLine = Readonly<{label: string; names: string}>;
export type TitleDetailsProps = Readonly<{
  /** `body` and `note`: on TV, About carries the full synopsis and the tagline beside the facts (detail-tv-4). */
  about?: Readonly<{title: string; rows: readonly Readonly<{label: string; values: readonly AboutValue[]}>[]; body?: string; note?: string}>;
  people?: Readonly<{title: string; groups: readonly (readonly CastPerson[])[]; seeAll?: Readonly<{label: string; onPress: () => void}>; lines?: readonly TitleCreditLine[];
    /** The grey note beside the heading ("Cast, then crew", detail-tv-4). */
    note?: string;
    /** TV: the row pages in place of See all (detail-tv-4 draws none); this reads the next page. */
    more?: () => void}>;
  /** The collection the title belongs to: the kit `CollectionBanner`, drawn at the panel's width (any width given is replaced). */
  collection?: CollectionCardProps;
}>;
/** A title's Details tab (desktop-detail f3, phone-detail d1 frame 2, tablet f6): About and Cast & crew, one above the
 * other on a phone and an upright tablet and side by side from a tablet on its side, then the collection. On TV
 * (detail-tv-4) About is the words beside the facts and Cast & crew one full-width row under it, so Down from a row of
 * posters lands on a person on every TV. The frame spaces the blocks; the page passes words and people only. */
export function TitleDetails({about, people, collection}: TitleDetailsProps) {
  const theme = useTheme(), frame = useFrameMetrics(), stacked = frame.form === 'phone' || frame.form === 'medium';
  if (frame.form === 'tv') return <TVTitleDetails about={about} people={people} collection={collection}/>;
  const cast = people && (people.groups.some(group => group.length > 0) || people.lines?.length) ? <View style={{flex: stacked ? undefined : sizes.detailsCastShare, minWidth: 0}}>
    <PanelHeading title={people.title} trailing={people.seeAll && <SeeAllLink label={people.seeAll.label} onPress={people.seeAll.onPress}/>}/>
    <CastRow groups={people.groups}/>
    {people.lines?.length ? <View style={{marginTop: theme.space.sm, gap: theme.space.tight}}>{people.lines.map(line => <Text key={line.label} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>
      {line.label} <Text style={{color: theme.colors.text}}>{line.names}</Text></Text>)}</View> : null}
  </View> : null;
  return <>
    {(about?.rows.length || cast) ? <PanelBlock><View style={{flexDirection: stacked ? 'column' : 'row', gap: frame.sectionGap}}>
      {about && about.rows.length > 0 && <View style={{flex: stacked ? undefined : 1, minWidth: 0}}><PanelHeading title={about.title}/><AboutTable rows={about.rows}/></View>}
      {cast}
    </View></PanelBlock> : null}
    {/* A phone draws the collection as its short link card at the card inset (phone-detail d1 `.colc`); wider forms as the banner (desktop-detail f3). */}
    {collection ? frame.form === 'phone'
      ? <View style={{marginHorizontal: frame.cardInset}}><CollectionTile {...collection} layout="link" width={frame.width - 2 * frame.cardInset}/></View>
      : <PanelBlock><CollectionBanner {...collection} width={frame.width - frame.gutter - frame.rightGutter}/></PanelBlock> : null}
  </>;
}

export type PeopleSheetProps = Readonly<{open: boolean; onOpenChange: (open: boolean) => void;
  /** The sheet's bar: its title (Cast & crew) and Back, named after the tab it returns to (Details): a title page's name
   * would be cut beside the centred title. */
  title: string; back: TopBarBack;
  groups: readonly (readonly CastPerson[])[];
  /** While the server has more people: the button that reads the next page (busy while it reads). */
  more?: Readonly<{label: string; loading: boolean; onPress: () => void}>;
  /** A page that failed to read, in one sentence; `more` offers it again. */
  note?: string}>;
/** Cast & crew's See all (phone-detail d1 frame 2, notes/detail-phone: "opens the full, paged people list"): everyone
 * on a full-height sheet, the cast then the crew under a rule, in wrapping rows of the same circles. */
export function PeopleSheet({open, onOpenChange, title, back, groups, more, note}: PeopleSheetProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <SheetFrame kind="full" open={open} onOpenChange={onOpenChange} bar={{title, back}}>
    <View style={{paddingHorizontal: frame.gutter, paddingTop: frame.headingGap, paddingBottom: frame.bottomClear, gap: frame.sectionGap}}>
      <CastRow groups={groups} wrap/>
      {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
      {more && <Button variant="text" size="small" align="center" disabled={more.loading} onPress={more.onPress}>{more.label}</Button>}
    </View>
  </SheetFrame>;
}

function TVTitleDetails({about, people, collection}: TitleDetailsProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  const words = about && (about.body !== undefined || about.note !== undefined);
  // Directed by and Written by are facts here, after Released (detail-tv-4), not lines under the cast.
  const lines = (people?.lines ?? []).filter(line => !about?.rows.some(row => row.label === line.label));
  const rows = about ? [...about.rows.slice(0, 1), ...lines.map(line => ({label: line.label, values: [{text: line.names}]})), ...about.rows.slice(1)] : [];
  return <>
    {about && (words || rows.length > 0) ? <PanelBlock><View style={{flexDirection: 'row', alignItems: 'flex-start', gap: sizes.tvAboutGap}}>
      <View style={{width: sizes.tvAboutWidth}}><PanelHeading title={about.title}/>
        {about.body !== undefined && <Text style={[theme.type.synopsis, {color: theme.colors.textSecondary}]}>{about.body}</Text>}
        {about.note !== undefined && <Text style={[theme.type.intro, {marginTop: theme.space.sm}]}>{about.note}</Text>}</View>
      {rows.length > 0 && <View style={{flex: 1, minWidth: 0}}><AboutTable rows={rows}/></View>}
    </View></PanelBlock> : null}
    {people && people.groups.some(group => group.length > 0) ? <View><PanelBlock><PanelHeading title={people.title} note={people.note}
      trailing={people.seeAll && <Button variant="text" size="small" trailingIcon="i-right" onPress={people.seeAll.onPress}>{people.seeAll.label}</Button>}/></PanelBlock>
      <PanelBlock><CastRow groups={people.groups} onEnd={people.more}/></PanelBlock></View> : null}
    {collection ? <PanelBlock><CollectionBanner {...collection} width={frame.width - frame.gutter - frame.rightGutter}/></PanelBlock> : null}
  </>;
}

/** One season (or an owner's episode group) in the TV season column (detail-tv-5a). Focus on it selects it at once; Hold
 * OK opens its options. */
export type TVSeasonItem = Readonly<{id: string; label: string; detail?: string; progress?: number; current: boolean; onFocus: () => void; onLongPress?: () => void}>;
/** detail-tv-5a and 5e: the show's seasons as a side list in its own window of six rows and half the seventh, the next row
 * peeking in when there are more; an edge fades only where the list goes on; the Hold OK hint under it while a season
 * holds focus. `onEnd` reads more seasons as focus nears the end. */
export function TVSeasonList({items, hint, onEnd}: Readonly<{items: readonly TVSeasonItem[]; hint?: Readonly<{key: string; label: string}>; onEnd?: () => void}>) {
  const theme = useTheme(), {edges, scroll} = useListEdges(), [focused, setFocused] = useState(false);
  const current = items.find(item => item.current)?.id ?? items[0]?.id;
  // Free, not vertical: a vertical region holds Up and Down at its ends, and the column has to let Up go back to the
  // buttons (through the frame's guide on tvOS) and Down on to the rows under it.
  return <FocusWithin onChange={setFocused}><FocusRegion id={`tv-seasons.${useId()}`} orientation="free" order={items.map(item => item.id)} entry={current}>
    <View style={{maxHeight: seasonColumn.window * (rowHeight + seasonColumn.rowGap)}}>
      <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={{gap: seasonColumn.rowGap}} {...scroll}>
        {items.map((item, index) => <SeasonListRow key={item.id} focusId={item.id} testID={`tv-season-${item.id}`} label={item.label} detail={item.detail} progress={item.progress} current={item.current}
          onFocus={() => {item.onFocus(); if (onEnd && index >= items.length - 3) onEnd();}} onPress={item.onFocus} onLongPress={item.onLongPress}/>)}
      </ScrollView>
      {edges.top && <ListEdgeFade testID="tv-seasons-fade-top" edge="top" length={seasonColumn.fade} opacity={seasonColumn.fadeOpacity}/>}
      {edges.bottom && <ListEdgeFade testID="tv-seasons-fade-bottom" edge="bottom" length={seasonColumn.fade} opacity={seasonColumn.fadeOpacity}/>}
    </View>
    {hint && focused && <View style={{marginTop: theme.space.md, marginLeft: theme.space.md}}><KeyHints hints={[hint]}/></View>}
  </FocusRegion></FocusWithin>;
}

/** One episode tile on TV (detail-tv-5b): the still (the show's backdrop when it has none), its length, watched mark,
 * Up next and progress, the title and one muted label. OK plays it; Hold OK opens its menu. */
export type TVEpisodeItem = Readonly<{key: string; title: string; label: EpisodeTileProps['label']; length?: string; status?: string; watched?: boolean; progress?: number;
  artwork?: ArtworkSource; backdropFallback?: boolean; onFocus: () => void; onPlay: () => void; onMore?: (anchor: React.RefObject<View | null>) => void}>;
/** What the band under the tiles describes: the season in focus, or the episode in focus (detail-tv-5a, 5b). Words only;
 * the band sets them. */
export type EpisodeBandData =
  | Readonly<{kind: 'season'; title: string; facts: readonly string[]; overview?: string}>
  | Readonly<{kind: 'episode'; title: string; facts: readonly FactPart[]; synopsis?: string; state?: Readonly<{label: string; progress?: number}>;
      guests?: Readonly<{label: string; people: readonly Readonly<{name: string; role?: string}>[]; more?: string}>; lines?: readonly Readonly<{label: string; names: string}>[]; hint?: Readonly<{key: string; label: string}>;
      error?: ReactNode}>;
/** detail-tv-5a and 5b: the season's episodes as one row of tiles (focus enters on the next-up episode), then the band
 * that describes whatever holds focus. `onEnd` reads the season's next page as the row nears its end. */
export function TVEpisodeShelf({items, entry, band, empty, onEnd}: Readonly<{items: readonly TVEpisodeItem[]; entry?: string; band?: EpisodeBandData; empty?: string; onEnd?: () => void}>) {
  const theme = useTheme(), width = seasonColumn.tile, region = `tv-episodes.${useId()}`, caption = cardCaptionHeight(theme, 2);
  // R1: a focused tile grows and draws its ring outside itself; the row keeps that room inside its scroller and the shelf
  // gives it back by the same amount, so the tiles stay on the column and the first one's ring and caption are never cut.
  const room = tvFocusRoom(width, width * 9 / 16 + caption, 'card').side;
  const onRange = (_first: number, last: number) => {if (onEnd && last >= items.length - 10) onEnd();};
  // The row brings the tile in focus to its start, so once it has moved on, the end of the tile before would show cut in
  // that room beside the season column. That tile is all but hidden until focus comes back to it. Every tile keeps its
  // own view (never flattened away when its opacity is 1): a tile re-parented as it took focus left tvOS unable to move
  // Left from it.
  const [at, setAt] = useState<string>(), before = items[items.findIndex(item => item.key === at) - 1]?.key;
  return <View style={{gap: theme.space.lg}}>
    {items.length === 0 && empty !== undefined && <Text testID="tv-episodes-empty" accessibilityRole="header" style={[theme.type.groupLabel, {color: theme.colors.textSecondary}]}>{empty}</Text>}
    {items.length > 0 && <View style={{marginHorizontal: -room}}><Row showHeading={false} title="" items={items} getKey={item => item.key} itemWidth={width} itemHeight={width * 9 / 16 + caption} captionHeight={caption}
      gutter={room} rightGutter={room} selection={resting} focus={{id: region, entry}} onVisibleRange={onRange}
      renderItem={(item, context) => <View collapsable={false} style={{opacity: item.key === before ? seasonColumn.passed : 1}}>
        <EpisodeItem item={item} focusId={context.focusId} testID={context.testID} onFocus={() => {context.onFocus?.(); setAt(item.key); item.onFocus();}} onBlur={context.onBlur} width={width}/></View>}/></View>}
    {band && <EpisodeBand band={band}/>}
  </View>;
}
const resting = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};
function EpisodeItem({item, width, ...focus}: Readonly<{item: TVEpisodeItem; width: number; focusId?: string; testID?: string; onFocus: () => void; onBlur?: () => void}>) {
  const anchor = useRef<View>(null), more = item.onMore;
  return <EpisodeTile {...focus} width={width} title={item.title} label={item.label} length={item.length} status={item.status} watched={item.watched} progress={item.progress}
    artwork={item.artwork} backdropFallback={item.backdropFallback} primaryAction="play" onPlay={item.onPlay} menuAnchorRef={anchor} onMore={more ? () => more(anchor) : undefined}/>;
}

/** The band under the episode row (detail-tv-5a: a season's facts over its overview; 5b: an episode's title, facts and
 * synopsis, its watch state, and beside them its guest stars and credits with the Hold OK hint). Every line wraps. */
export function EpisodeBand({band}: Readonly<{band: EpisodeBandData}>) {
  const theme = useTheme();
  if (band.kind === 'season') return <View testID="tv-episode-band" style={{gap: theme.space.xs}}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.md, rowGap: theme.space.xs}}>
      <Text accessibilityRole="header" style={[theme.type.rowLabel, {color: theme.colors.text}]}>{band.title}</Text>
      {band.facts.map(fact => <Text key={fact} style={[theme.type.meta, {color: theme.colors.textTertiary}]}>{fact}</Text>)}
    </View>
    {band.overview !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{band.overview}</Text>}
  </View>;
  const track = theme.spec.card.progress.height[theme.column];
  return <View testID="tv-episode-band" style={{flexDirection: 'row', gap: theme.space.md}}>
    <View style={{flex: seasonColumn.bandWords, minWidth: 0, gap: theme.space.sm}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{band.title}</Text>
      {/* The hero's facts line: the rating is its outlined chip (content-rating). */}
      <HeroMetadata facts={band.facts}/>
      {band.synopsis !== undefined && <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{band.synopsis}</Text>}
      {band.state && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
        {band.state.progress !== undefined && <View style={{width: seasonColumn.bandBar, height: track, borderRadius: track, backgroundColor: alpha(theme.colors.text, theme.spec.card.progress.trackAlpha)}}>
          <View style={{height: track, width: `${Math.min(1, band.state.progress) * 100}%`, borderRadius: track, backgroundColor: theme.colors.accentStrong}}/></View>}
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{band.state.label}</Text>
      </View>}
      {band.error}
    </View>
    <View style={{flex: seasonColumn.bandCredits, minWidth: 0, gap: theme.space.sm}}>
      {/* Each credit is a label and its names in two columns (detail-tv-5b: Guest stars, Directed by, Written by). */}
      {band.guests && band.guests.people.length > 0 && <View style={{flexDirection: 'row', gap: theme.space.sm}}>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, width: sizes.aboutLabel[theme.form]}]}>{band.guests.label}</Text>
        <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>
          {band.guests.people.map(person => <Text key={person.name} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{person.name}
            {person.role !== undefined && <Text style={{color: theme.colors.textTertiary}}>{'  '}{person.role}</Text>}</Text>)}
          {band.guests.more !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{band.guests.more}</Text>}
        </View>
      </View>}
      {band.lines?.map(line => <View key={line.label} style={{flexDirection: 'row', gap: theme.space.sm}}>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, width: sizes.aboutLabel[theme.form]}]}>{line.label}</Text>
        <Text style={[theme.type.rowLabel, {flex: 1, minWidth: 0, color: theme.colors.text}]}>{line.names}</Text></View>)}
      {band.hint && <View style={{marginTop: theme.space.sm}}><KeyHints hints={[band.hint]}/></View>}
    </View>
  </View>;
}
