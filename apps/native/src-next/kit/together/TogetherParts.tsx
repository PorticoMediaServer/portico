import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {AmbientArt, Artwork, ProfileFace, type AmbientArtSource, type ArtworkSource, type ProfileOption} from '../artwork';
import {Focusable} from '../controls';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

/** One face in Watch Together: a member's initial and colour (the viewer's own picture), or a private member's plain glyph. */
export type TogetherFace = Readonly<{id: string; private: true}> | Readonly<{id: string; private: false; initial: string;
  color?: ProfileOption['color']; source?: ProfileOption['source']}>;

const faceSize = 36, overlap = 9;

function Face({face, privateLabel}: {face: TogetherFace; privateLabel: string}) {
  const theme = useTheme();
  return face.private
    ? <View accessibilityLabel={privateLabel} style={{width: faceSize, height: faceSize, borderRadius: faceSize / 2, alignItems: 'center',
        justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}><Icon name="i-user" size={faceSize / 2} color={theme.colors.textSecondary}/></View>
    : <ProfileFace size={faceSize} initial={face.initial} color={face.color} source={face.source} scaleOnFocus={false}/>;
}

/** The faces of who is here (states 27a): overlapping, each ringed in the card's colour. */
export function TogetherFaces({faces, privateLabel}: {faces: readonly TogetherFace[]; privateLabel: string}) {
  const theme = useTheme();
  return <View testID="together-lobby-faces" style={{flexDirection: 'row'}}>
    {faces.map((face, index) => <View key={face.id} style={{marginLeft: index ? -overlap : 0, borderRadius: faceSize / 2 + 2.5,
      borderWidth: 2.5, borderColor: theme.colors.raised}}><Face face={face} privateLabel={privateLabel}/></View>)}
  </View>;
}

/** The invitation to start or join (states 27a): a raised card washed in what is on, the faces, the headline, a line of help,
 * then the actions stacked full width. */
export function TogetherInvitation({art, faces, title, body, children}: {art?: AmbientArtSource; faces: ReactNode; title: string; body: string;
  children: ReactNode}) {
  const theme = useTheme();
  return <View style={{marginHorizontal: theme.space.sm, borderRadius: 18, overflow: 'hidden', backgroundColor: theme.colors.raised,
    padding: theme.space.sm + 2, gap: theme.space.sm - 2}}>
    {art && <AmbientArt testID="together-lobby-art" source={art} treatment="wash"/>}
    {faces}
    <View style={{gap: theme.space.xs - 2}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{body}</Text>
    </View>
    <View style={{gap: theme.space.controlGap}}>{children}</View>
  </View>;
}

/** What is on in the room (states 27c): its picture, a dot for the host (green here, amber away), "Playing", the title, the
 * position over a thin bar, and who is in step. */
export function TogetherNowCard({art, hostPresent, label, title, position, progress, status, children}: {art?: ArtworkSource; hostPresent: boolean;
  label: string; title: string; position?: string; progress?: number; status: string; children?: ReactNode}) {
  const theme = useTheme();
  return <View testID="together-now-card" style={{marginHorizontal: theme.space.sm, borderRadius: 16, overflow: 'hidden', minHeight: 196,
    justifyContent: 'flex-end', padding: theme.space.sm - 2, backgroundColor: theme.colors.raised}}>
    {art && <View style={{position: 'absolute', inset: 0}}><Artwork source={art}/>
      <View style={{position: 'absolute', inset: 0, backgroundColor: alpha(theme.colors.ink, .55)}}/></View>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.tight}}>
      <View style={{width: 7, height: 7, borderRadius: 4, backgroundColor: hostPresent ? theme.colors.healthy : theme.colors.amber}}/>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{label}</Text>
    </View>
    <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
    {position !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}]}>{position}</Text>}
    {progress !== undefined && <View style={{height: 3, borderRadius: 2, backgroundColor: alpha(theme.colors.text, .16), marginTop: theme.space.controlGap,
      overflow: 'hidden'}}><View style={{height: 3, width: `${Math.max(0, Math.min(1, progress)) * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: theme.space.tight}]}>{status}</Text>
    {children}
  </View>;
}

/** One person in the room (states 27c): their face, name ("Demo (you)"), what they chose to be called, and Host or in step. */
export function TogetherMemberRow({face, privateLabel, name, note, status, inStep, first, onPress, testID, accessibilityLabel}: {face: TogetherFace;
  privateLabel: string; name: string; note?: string; status: string; /** In step reads green; away, loading or catching up amber. */ inStep: boolean; first: boolean; onPress?: () => void; testID: string; accessibilityLabel: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole={onPress ? 'button' : 'text'} accessibilityLabel={accessibilityLabel} onPress={onPress}
    focusKind="row" focusRadius={0} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4, paddingHorizontal: 14,
      minHeight: 52, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}}>
    <Face face={face} privateLabel={privateLabel}/>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{name}</Text>
      {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{note}</Text>}
    </View>
    <Text style={[theme.type.captionStrong, {color: inStep ? theme.colors.healthy : theme.colors.amber}]}>{status}</Text>
  </Focusable>;
}

/** The invite code card (states 27c): the code large and spaced, what it is for, then Share code and New code. */
export function TogetherInviteCode({code, note, children}: {code?: string; note?: string; children: ReactNode}) {
  const theme = useTheme();
  return <View testID="together-invite-card" style={{padding: theme.space.sm, borderRadius: 14,
    backgroundColor: theme.colors.raised, gap: theme.space.xs}}>
    {code !== undefined && <Text style={[theme.type.title, {color: theme.colors.text, letterSpacing: 4, fontVariant: ['tabular-nums']}]}>{code}</Text>}
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{note}</Text>}
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>{children}</View>
  </View>;
}

/** Full-width buttons stacked under a card (Start a group, Join with a code; Pause for everyone; Leave, End for everyone). */
export function TogetherActions({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{marginHorizontal: theme.space.sm, gap: theme.space.controlGap}}>{children}</View>;
}
