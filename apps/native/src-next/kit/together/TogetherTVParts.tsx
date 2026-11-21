import React, {forwardRef, type ReactNode} from 'react';
import {FlatList, ScrollView, Text, View, type FlatListProps} from 'react-native';
import {frameSpec} from '@portico/design';
import {AmbientArt, Artwork, ProfileFace, type AmbientArtSource, type ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {useFrameMetrics} from '../frames/metrics';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import type {TogetherFace} from './TogetherParts';

const spec = frameSpec.together.tv;
type Region = Omit<FocusRegionProps, 'children' | 'style'>;

function Face({face, size, privateLabel}: {face: TogetherFace; size: number; privateLabel: string}) {
  const theme = useTheme();
  return face.private
    ? <View accessibilityLabel={privateLabel} style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center',
        backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={size / 2} color={theme.colors.textSecondary}/></View>
    : <ProfileFace size={size} initial={face.initial} color={face.color} source={face.source} scaleOnFocus={false}/>;
}

/** The lobby's invitation on TV (tv-more 11): a raised card washed in what is on, the faces of who is here overlapping, the
 * headline, a line of help, then Start a group and Join with a code side by side. */
export function TogetherTVInvitation({art, faces, privateLabel, title, body, children}: {art?: AmbientArtSource; faces: readonly TogetherFace[];
  privateLabel: string; title: string; body: string; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics(), ring = theme.space.hairline * 3;
  // tv-more 10 `.wt-card`: up to 1180 wide (the page's room), padded 48 / 56 / 52; 26 under the faces, 14 under the title and
  // 38 under the words (`h2`, `p` margins); the words 26/1.5 to 820.
  return <View style={{maxWidth: spec.card, marginLeft: frame.cardInset, marginRight: frame.cardInset, borderRadius: frame.cardRadius + theme.space.xs, overflow: 'hidden', backgroundColor: theme.colors.raised,
    paddingTop: spec.cardPad.top, paddingHorizontal: spec.cardPad.x, paddingBottom: spec.cardPad.bottom}}>
    {art && <AmbientArt testID="together-lobby-art" source={art} treatment="wash"/>}
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: frame.cardRadius + theme.space.xs, borderWidth: theme.space.hairline * 1.5, borderColor: alpha(theme.colors.text, .08)}}/>
    <View testID="together-lobby-faces" style={{flexDirection: 'row'}}>
      {faces.map((face, index) => <View key={face.id} style={{marginLeft: index ? -spec.faceOverlap : 0, borderRadius: spec.lobbyFace / 2 + ring,
        borderWidth: ring, borderColor: theme.colors.raised}}><Face face={face} size={spec.lobbyFace} privateLabel={privateLabel}/></View>)}
    </View>
    <View style={{marginTop: spec.cardGaps.faces, marginBottom: spec.cardGaps.body, gap: spec.cardGaps.title}}>
      <Text accessibilityRole="header" style={[theme.type.display, {color: theme.colors.text, maxWidth: spec.nowWidth + theme.space.xl * 2}]}>{title}</Text>
      <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary, maxWidth: spec.bodyMax}]}>{body}</Text>
    </View>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.controlGap}}>{children}</View>
  </View>;
}

/** The line over a side sheet's title ("Watch Together", tv-more 12, 13). */
export function TogetherTVEyebrow({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.eyebrow, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** A field in Start a group or Join with a code on TV (tv-more 12, 13): its label, the value in a box that takes focus (OK opens
 * the keyboard beside it), and a line of help under it. */
export const TogetherTVField = forwardRef<View, {focusId: string; testID: string; label: string; description?: string; value: string; onPress: () => void}>(
  function TogetherTVField({focusId, testID, label, description, value, onPress}, ref) {
    const theme = useTheme();
    return <View style={{gap: theme.space.xs}}>
      <Text style={[theme.type.sectionLabel, {color: theme.colors.textTertiary}]}>{label}</Text>
      <Focusable ref={ref} focusId={focusId} testID={testID} accessibilityRole="button" accessibilityLabel={label} accessibilityValue={{text: value}} onPress={onPress} focusKind="row"
        style={{height: spec.field, paddingHorizontal: theme.space.md, justifyContent: 'center', borderRadius: theme.baseRadii.control,
          borderWidth: theme.space.hairline * 1.5, borderColor: theme.colors.lineStrong, backgroundColor: alpha(theme.colors.text, .08)}}>
        <Text style={[theme.type.heading, {color: theme.colors.text}]}>{value}</Text>
      </Focusable>
      {description !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{description}</Text>}
    </View>;
  });

/** Start a group's or Join with a code's fields and, while one is typed into, the keyboard beside them (tv-more 12, 13). */
export function TogetherTVForm({region, fields, keyboard}: {region: Region; fields: ReactNode; keyboard?: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: keyboard ? 'row' : 'column', gap: theme.space.xl}}>
    <FocusRegion {...region} style={{flex: 1, gap: theme.space.md}}>{fields}</FocusRegion>{keyboard}</View>;
}

/** A labelled choice in the form: "Who can play, pause and choose what's on" over Only me / Everyone. */
export function TogetherTVChoice({label, children}: {label: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs, alignSelf: 'stretch'}}>
    <Text style={[theme.type.sectionLabel, {color: theme.colors.textTertiary}]}>{label}</Text>
    {children}
  </View>;
}

/** A Watch Together room on TV (tv-more 14, 15): "Watch Together" over the group's name, its notices, then what is on with
 * its controls and the queue at the left, the people and the invite at the right, and Leave group / End for everyone at the foot. */
export function TogetherTVRoomFrame({kicker, title, notices, main, side, footer}: {kicker: string; title: string; notices?: ReactNode;
  main: ReactNode; side: ReactNode; footer: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flex: 1, paddingLeft: frame.gutter, paddingRight: frame.rightGutter, paddingTop: frame.titleTop, paddingBottom: theme.space.xl}}>
    <Text style={[theme.type.eyebrow, {color: theme.colors.textSecondary}]}>{kicker}</Text>
    <Text accessibilityRole="header" style={[theme.type.listTitle, {color: theme.colors.text}]}>{title}</Text>
    {notices && <View style={{gap: theme.space.sm, marginTop: theme.space.md}}>{notices}</View>}
    <View style={{flex: 1, flexDirection: 'row', gap: spec.columnsGap, marginTop: theme.space.lg}}>
      <View style={{width: spec.nowWidth, gap: theme.space.md}}>{main}</View>
      <ScrollView style={{width: spec.sideWidth}} contentContainerStyle={{gap: theme.space.md}}>{side}</ScrollView>
    </View>
    {footer}
  </View>;
}

/** What is on in the room (tv-more 14): its picture, "Playing" (amber while the host is away), the title, its facts and the
 * position, how many are ready, and the bar. */
export function TogetherTVNowCard({art, away, label, title, facts, readiness, progress}: {art?: ArtworkSource; away: boolean; label: string;
  title: string; facts: readonly string[]; readiness: readonly string[]; progress?: number}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View testID="together-now-card" style={{height: spec.nowHeight, borderRadius: frame.cardRadius, overflow: 'hidden', backgroundColor: theme.colors.raised,
    justifyContent: 'flex-end', paddingHorizontal: theme.space.lg, paddingBottom: theme.space.md}}>
    {art && <View style={{position: 'absolute', inset: 0, opacity: .62}}><Artwork source={art}/></View>}
    <Text style={[theme.type.captionStrong, {color: away ? theme.colors.amber : theme.colors.healthy, marginBottom: theme.space.xs}]}>{label}</Text>
    <Text style={[theme.type.title, {color: theme.colors.text}]}>{title}</Text>
    <Facts parts={facts} role="body"/>
    <Facts parts={readiness} role="caption" tertiary/>
    {progress !== undefined && <View style={{height: theme.space.hairline * 6, borderRadius: theme.space.hairline * 3, backgroundColor: alpha(theme.colors.text, .2),
      marginTop: theme.space.sm, overflow: 'hidden'}}><View style={{height: '100%', width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
  </View>;
}

/** Facts separated by space, never a middle dot (the approved frames). */
function Facts({parts, role, tertiary = false}: {parts: readonly string[]; role: 'body' | 'caption'; tertiary?: boolean}) {
  const theme = useTheme();
  if (!parts.length) return null;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.md, marginTop: theme.space.hairline * 4}}>
    {parts.map((part, index) => <Text key={index} style={[theme.type[role], {color: tertiary ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{part}</Text>)}
  </View>;
}

/** A row of the room's buttons: Pause for everyone and Open player (or Become host); a member's line that the host controls. */
export function TogetherTVControls({region, children}: {region: Region; children: ReactNode}) {
  const theme = useTheme();
  return <FocusRegion {...region} orientation="horizontal" style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, minHeight: spec.field}}>{children}</FocusRegion>;
}

/** A quiet line in place of controls ("The host controls playback"). */
export function TogetherTVNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginLeft: theme.space.sm}]}>{children}</Text>;
}

/** A labelled part of the room: Queue, 3 people, Invite people. */
export function TogetherTVSection({label, children}: {label: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.sm, flexShrink: 1}}>
    <Text style={[theme.type.sectionLabel, {color: theme.colors.textTertiary}]}>{label}</Text>
    {children}
  </View>;
}

export type TogetherTVQueueItem = Readonly<{key: string; number: number; current: boolean; title: string; facts: readonly string[]; still?: ArtworkSource;
  onPress: () => void; ref: (node: View | null) => void}>;

/** The queue (tv-more 14): one raised list, each row its number (a play mark on the one on now), its still, title and facts,
 * and "Now playing". Only the rows in view are read; the list reports which. */
export function TogetherTVQueue<T>({region, data, keyOf, item, onViewableItemsChanged, empty, nowPlaying}: {region: Region; data: readonly T[]; keyOf: (value: T) => string;
  item: (value: T, index: number) => TogetherTVQueueItem; onViewableItemsChanged: FlatListProps<T>['onViewableItemsChanged']; empty: string; nowPlaying: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <FocusRegion {...region} style={{flexShrink: 1}}>
    {data.length ? <FlatList data={data} keyExtractor={keyOf} onViewableItemsChanged={onViewableItemsChanged} initialNumToRender={4} maxToRenderPerBatch={4} windowSize={3}
      removeClippedSubviews={false} getItemLayout={(_, index) => ({index, length: spec.queueRow, offset: index * spec.queueRow})}
      style={{flexGrow: 0, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}
      renderItem={({item: value, index}) => {
        const row = item(value, index);
        return <Focusable ref={row.ref} focusId={row.key} testID={`together-queue-${row.key}`} accessibilityRole="button" accessibilityLabel={row.title} focusKind="row" onPress={row.onPress}
          style={{height: spec.queueRow, flexDirection: 'row', alignItems: 'center', gap: theme.space.md, paddingHorizontal: theme.space.md,
            borderTopWidth: index ? theme.space.hairline : 0, borderTopColor: theme.colors.lineSoft}}>
          <View style={{width: theme.space.lg, alignItems: 'center'}}>{row.current ? <Icon name="i-play" size={theme.type.caption.fontSize} color={theme.colors.textTertiary}/>
            : <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{row.number}</Text>}</View>
          {row.still && <View style={{width: spec.queueStill.width, height: spec.queueStill.height, borderRadius: theme.space.hairline * 7, overflow: 'hidden'}}><Artwork source={row.still}/></View>}
          <View style={{flex: 1, minWidth: 0}}>
            <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{row.title}</Text>
            {row.facts.length > 0 && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{row.facts.join('   ')}</Text>}
          </View>
          {row.current && <Text style={[theme.type.captionStrong, {color: theme.colors.accentStrong}]}>{nowPlaying}</Text>}
        </Focusable>;
      }}/>
      : <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{empty}</Text>}
  </FocusRegion>;
}

export type TogetherTVMember = Readonly<{key: string; name: string; note?: string; host?: string; face: TogetherFace; status: string;
  tone: 'good' | 'warn' | 'quiet'; accessibilityLabel: string; onPress?: () => void; ref: (node: View | null) => void}>;

/** The people in the room (tv-more 14): a face, the name ("Demo (you)") with what they chose to be called and Host under it,
 * and in step (green), catching up (amber) or away. A host presses an in-step member to make them host. */
export function TogetherTVPeople<T>({region, data, keyOf, item, privateLabel}: {region: Region; data: readonly T[]; keyOf: (value: T) => string;
  item: (value: T) => TogetherTVMember; privateLabel: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <FocusRegion {...region}>
    <FlatList data={data} keyExtractor={keyOf} initialNumToRender={4} maxToRenderPerBatch={4} windowSize={3} removeClippedSubviews={false} scrollEnabled={false}
      style={{flexGrow: 0, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised}}
      renderItem={({item: value, index}) => {
        const row = item(value);
        return <Focusable ref={row.ref} focusId={row.key} testID={`together-member-${row.key}`} accessibilityRole={row.onPress ? 'button' : 'text'} accessibilityLabel={row.accessibilityLabel}
          focusKind="row" onPress={row.onPress} style={{minHeight: spec.memberRow, flexDirection: 'row', alignItems: 'center', gap: theme.space.md, paddingHorizontal: theme.space.md, paddingVertical: theme.space.sm,
            borderTopWidth: index ? theme.space.hairline : 0, borderTopColor: theme.colors.lineSoft}}>
          <Face face={row.face} size={spec.memberFace} privateLabel={privateLabel}/>
          <View style={{flex: 1, minWidth: 0}}>
            <Text style={[theme.type.wideTitle, {color: theme.colors.text}]}>{row.name}</Text>
            {row.note !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{row.note}</Text>}
            {row.host !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{row.host}</Text>}
          </View>
          <Text style={[theme.type.captionStrong, {color: row.tone === 'good' ? theme.colors.healthy : row.tone === 'warn' ? theme.colors.amber : theme.colors.textTertiary}]}>{row.status}</Text>
        </Focusable>;
      }}/>
  </FocusRegion>;
}

/** The invite code (tv-more 14): the code large and spaced in two halves, what it is for, then New code. */
export function TogetherTVInviteCode({code, note, children}: {code?: string; note?: string; children: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View testID="together-invite-card" style={{borderRadius: frame.cardRadius, padding: theme.space.lg, backgroundColor: theme.colors.raised, gap: theme.space.sm}}>
    {code !== undefined && <Text style={[theme.type.display, {color: theme.colors.text, letterSpacing: (theme.type.display.fontSize ?? 0) * .14, fontVariant: ['tabular-nums']}]}>{code}</Text>}
    {note !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{note}</Text>}
    <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>{children}</View>
  </View>;
}

/** Leave group and End for everyone at the room's foot. */
export function TogetherTVFooter({region, children}: {region: Region; children: ReactNode}) {
  const theme = useTheme();
  return <FocusRegion {...region} orientation="horizontal" style={{flexDirection: 'row', gap: theme.space.controlGap, marginTop: theme.space.md}}>{children}</FocusRegion>;
}
