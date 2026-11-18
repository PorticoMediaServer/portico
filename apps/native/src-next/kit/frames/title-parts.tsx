import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {Artwork, PersonCircle, type ArtworkSource} from '../artwork';
import {Button, Focusable} from '../controls';
import {Icon, type IconName} from '../Icon';
import {alpha} from '../shell/visual';
import {TVRow} from '../layout/TVRow';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

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
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone';
  return <View style={phone ? {borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised, paddingHorizontal: theme.space.sm} : {gap: rowPad}}>
    {rows.map((row, index) => <View key={row.label} style={{flexDirection: 'row', gap: theme.space.sm, paddingVertical: phone ? rowPad : 0, borderTopWidth: phone && index > 0 ? 1 : 0, borderTopColor: theme.colors.line}}>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, width: phone ? 104 : 140}]}>{row.label}</Text>{/* R5 interim: the label column (desktop-detail f3 140, phone-detail d1 104) */}
      <Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.text, fontFamily: phone ? theme.type.rowLabel.fontFamily : undefined}]}>{row.values.map((value, position) => <Text key={position}>{position > 0 ? ', ' : ''}
        {value.onPress ? <Text accessibilityRole="link" onPress={value.onPress} style={{textDecorationLine: 'underline', textDecorationColor: alpha(theme.colors.text, .4), color: phone ? theme.colors.accentStrong : theme.colors.text}}>{value.text}</Text> : value.text}</Text>)}</Text>
    </View>)}
  </View>;
}

export type CastPerson = Readonly<{id: string; name: string; role?: string; portrait?: ArtworkSource; onPress?: () => void}>;
const initials = (name: string) => name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join('');
/** Cast and crew as circles with the name and role under each (desktop-detail f3, phone-detail d1 frame 2, foldable f2,
 * detail-tv-4): initials when there is no photo. A row that scrolls sideways on a phone and TV; the cast, then the crew
 * under a rule, wrapping, on a tablet. */
export function CastRow({groups}: Readonly<{groups: readonly (readonly CastPerson[])[]}>) {
  const theme = useTheme(), frame = useFrameMetrics(), scroll = frame.form === 'phone' || frame.form === 'tv';
  // R4/R5 interim: the circle sizes drawn per form (phone-detail 64, tablet and web 96, TV 150).
  const size = frame.form === 'phone' ? 64 : frame.form === 'tv' ? 150 : 96, width = size + (frame.form === 'tv' ? 50 : frame.form === 'phone' ? 14 : 24);
  const person = (entry: CastPerson) => <Focusable key={entry.id} focusKind="card" focusRadius={size / 2} disabled={!entry.onPress} focusableWhenDisabled={false} accessibilityRole="button"
    accessibilityLabel={[entry.name, entry.role].filter(Boolean).join(', ')} onPress={entry.onPress} style={{width, alignItems: 'center'}}>
    <PersonCircle size={size} initials={initials(entry.name)} source={entry.portrait} covers={[]} alt=""/>
    <Text style={[theme.type.cardTitle, {marginTop: theme.space.controlGap, textAlign: 'center', color: theme.colors.text}]}>{entry.name}</Text>
    {entry.role !== undefined && <Text style={[theme.type.cardSubtitle, {marginTop: 2, textAlign: 'center', color: theme.colors.textTertiary}]}>{entry.role}</Text>}
  </Focusable>;
  const lists = groups.filter(group => group.length > 0);
  if (scroll) return <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap: rowPad, alignItems: 'flex-start'}}>{lists.flat().map(person)}</ScrollView></TVRow>;
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
  const theme = useTheme(), frame = useFrameMetrics(), phone = frame.form === 'phone', art = phone ? 56 : frame.form === 'tv' ? 140 : 106;
  return <Focusable focusKind="row" focusRadius={frame.cardRadius} accessibilityRole="button" accessibilityLabel={title} onPress={onPress} disabled={!onPress} focusableWhenDisabled={false}
    style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, paddingVertical: rowPad}}>
    {!phone && <Text style={[theme.type.pageTitle, {width: 56, textAlign: 'center', color: alpha(theme.colors.text, .3)}]}>{number}</Text>}
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
    <View style={{width: 112, height: 63, borderRadius: 8, overflow: 'hidden', backgroundColor: theme.colors.raised}}><Artwork source={artwork} width={112} height={63}/></View>
    <View style={{flex: 1, minWidth: 0, gap: 2}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 2}}>{direction === 'previous' && <Icon name="i-left" size={14} color={theme.colors.textTertiary}/>}
        <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{label}</Text>{direction === 'next' && <Icon name="i-right" size={14} color={theme.colors.textTertiary}/>}</View>
      <Text style={[theme.type.cardTitle, {color: theme.colors.text}]}>{title}</Text>
      {detail !== undefined && <Text style={[theme.type.cardSubtitle, {color: theme.colors.textTertiary}]}>{detail}</Text>}
    </View>
  </Focusable>;
}
