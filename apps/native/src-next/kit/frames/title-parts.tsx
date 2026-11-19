import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Artwork, type ArtworkSource} from '../artwork';
import {CircleCard, CollectionBanner, CollectionTile, type CollectionCardProps} from '../cards';
import {Button, Focusable, Segmented} from '../controls';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {TVRow} from '../layout/TVRow';
import {SeeAllLink} from '../layout/Row';
import {useTheme} from '../theme';
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
export function PanelHeading({title, count, note, trailing}: Readonly<{title: string; count?: string; note?: string; trailing?: ReactNode}>) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.sm, rowGap: theme.space.controlGap, marginBottom: frame.headingGap}}>
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}{count !== undefined ? <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{'  '}{count}</Text> : null}</Text>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
    {trailing && <View style={{marginLeft: 'auto', flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.controlGap}}>{trailing}</View>}
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
export function CastRow({groups, wrap = false}: Readonly<{groups: readonly (readonly CastPerson[])[]; wrap?: boolean}>) {
  const theme = useTheme(), frame = useFrameMetrics(), scroll = !wrap && (frame.form === 'phone' || frame.form === 'tv');
  const size = sizes.castCircle[frame.form], width = size + (frame.form === 'tv' ? 50 : frame.form === 'phone' ? 14 : 24);
  // The kit's person card: the ring wraps only the circle and the whole person grows on focus (detail-tv-4); the name and
  // role take the column's width under the circle.
  // A phone sets the name and the role small, as drawn (phone-detail d1 frame 2 `.pp`).
  const phone = frame.form === 'phone', names = phone ? {titleTextStyle: theme.type.castName, captionTextStyle: theme.type.castRole} : {};
  const person = (entry: CastPerson) => <CircleCard key={entry.id} {...names} focusId={`cast-${entry.id}`} person width={width} circle={size} title={entry.name} titleLines={2} captionLines={2}
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
  const theme = useTheme();
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
export const seasonColumn = {width: 330, gap: 70, rowGap: 6} as const;

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
  about?: Readonly<{title: string; rows: readonly Readonly<{label: string; values: readonly AboutValue[]}>[]}>;
  people?: Readonly<{title: string; groups: readonly (readonly CastPerson[])[]; seeAll?: Readonly<{label: string; onPress: () => void}>; lines?: readonly TitleCreditLine[]}>;
  /** The collection the title belongs to: the kit `CollectionBanner`, drawn at the panel's width (any width given is replaced). */
  collection?: CollectionCardProps;
}>;
/** A title's Details tab (desktop-detail f3, phone-detail d1 frame 2, tablet f6): About and Cast & crew, one above the
 * other on a phone and an upright tablet and side by side from a tablet on its side, then the collection. The frame
 * spaces the blocks; the page passes words and people only. */
export function TitleDetails({about, people, collection}: TitleDetailsProps) {
  const theme = useTheme(), frame = useFrameMetrics(), stacked = frame.form === 'phone' || frame.form === 'medium';
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
