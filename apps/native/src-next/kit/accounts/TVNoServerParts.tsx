import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {tvAccountSpec} from '@portico/design';
import QRCode from 'react-native-qrcode-svg';
import {Focusable} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {Icon, type IconName} from '../Icon';
import {CountdownRing} from '../states';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';
import {shellLayout} from '../../shell/model';

const wide = tvAccountSpec.wide;
type Region = Omit<FocusRegionProps, 'children' | 'style'>;

/** connect 35 to 38: a Portico Account signed in on a TV with nowhere to go yet. The 1920x1080 canvas, then at the gutter the
 * accent kicker ("Your Portico Account is ready"), the account title and its lede, a notice under them when there is one
 * (connect 38), then the page's own content and its foot. The column scrolls, so nothing is clipped at the bottom. */
export function TVAccountWidePage({kicker, title, body, notice, children, foot, testID}: {kicker: string; title: string; body?: string;
  notice?: ReactNode; children: ReactNode; foot?: ReactNode; testID?: string}) {
  const theme = useTheme(), window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  return <View testID={testID} style={{flex: 1}}>
    <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height, transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      <ScrollView style={{flex: 1}} contentContainerStyle={{paddingLeft: wide.left, paddingRight: wide.right, paddingTop: wide.top, paddingBottom: wide.top}}>
        <Text style={[theme.type.eyebrow, {color: theme.colors.accentStrong, marginBottom: tvAccountSpec.eyebrowGap}]}>{kicker}</Text>
        <Text accessibilityRole="header" style={[theme.type.accountTitle, {color: theme.colors.text, marginBottom: wide.titleGap}]}>{title}</Text>
        {body !== undefined && <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary, maxWidth: wide.ledeMax}]}>{body}</Text>}
        {notice && <View style={{marginTop: wide.noticeGap, gap: tvAccountSpec.fieldGap}}>{notice}</View>}
        <View style={{marginTop: wide.sectionGap}}>{children}</View>
        {foot}
      </ScrollView>
    </View>
  </View>;
}

/** Why the account is here (connect 38): an info glyph, the bold reason ("You no longer have access to The cottage"), what it
 * means, and Try again when the reason can be retried. */
export function TVAccountNotice({title, body, action}: {title: string; body: string; action?: ReactNode}) {
  const theme = useTheme();
  return <View accessibilityRole="alert" style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.md, paddingHorizontal: theme.space.lg, paddingVertical: theme.space.md,
    borderRadius: theme.baseRadii.control * 2, backgroundColor: theme.colors.raised, borderWidth: theme.space.hairline * 1.5, borderColor: theme.colors.lineSoft}}>
    <Icon name="i-info" size={theme.type.body.fontSize} color={theme.colors.accentStrong}/>
    <Text style={[theme.type.body, {flex: 1, color: theme.colors.textSecondary}]}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}.</Text> {body}</Text>
    {action}
  </View>;
}

/** The two doors side by side (connect 35, 38): Join with an invitation, Set up your own server. */
export function TVDoors({region, children}: {region: Region; children: ReactNode}) {
  return <FocusRegion {...region} orientation="horizontal" style={{flexDirection: 'row', gap: wide.door.gap, alignItems: 'stretch'}}>{children}</FocusRegion>;
}

/** The page's links under the doors (Find a server on this Wi-Fi, Sign out of Portico Account), far enough apart that one's
 * focus ring never covers the other's words. */
export function TVAccountLinks({region, children}: {region: Region; children: ReactNode}) {
  return <FocusRegion {...region} orientation="horizontal"
    style={{flexDirection: 'row', flexWrap: 'wrap', gap: wide.linkGap, marginTop: wide.sectionGap}}>{children}</FocusRegion>;
}

/** One door: its glyph in a round chip, its title and what it does; the set-up door carries its download code at the right. */
/** `short`: under a notice (connect 38) the doors are lower and the code smaller, so the links under them stay inside the TV's
 * safe area. */
export function TVDoor({focusId, icon, title, body, code: codeUrl, codeLabel, disabled, onPress, accessibilityLabel, short = false}: {
  focusId: string; icon: IconName; title: string; body: string; code?: string; codeLabel?: string; disabled?: boolean;
  onPress?: () => void; accessibilityLabel?: string; short?: boolean}) {
  const theme = useTheme(), door = wide.door, height = short ? door.short.height : door.height, code = tvDoorCode(short);
  return <Focusable focusId={focusId} focusKind="card" focusRadius={door.radius} disabled={disabled} focusableWhenDisabled onPress={onPress} accessibilityRole="button"
    accessibilityLabel={accessibilityLabel ?? title} style={{flex: 1, minHeight: height, padding: door.padding, borderRadius: door.radius,
      backgroundColor: theme.colors.raised, borderWidth: theme.space.hairline * 1.5, borderColor: theme.colors.lineSoft}}>
    <View style={{width: door.chip, height: door.chip, borderRadius: door.chip / 2, alignItems: 'center', justifyContent: 'center', marginBottom: theme.space.md,
      backgroundColor: alpha(theme.colors.accent, .16)}}><Icon name={icon} size={door.glyph} color={theme.colors.accentStrong}/></View>
    {/* The title runs the door's full width (connect 35); the set-up door's code sits beside the line under it. */}
    <Text style={[theme.type.title, {color: theme.colors.text, marginBottom: theme.space.sm}]}>{title}</Text>
    <View style={{flexDirection: 'row', gap: theme.space.lg}}>
      <Text style={[theme.type.accountLede, {flex: 1, color: theme.colors.textSecondary}]}>{body}</Text>
      {codeUrl && <View accessible accessibilityLabel={codeLabel ?? codeUrl} style={{width: code, height: code, padding: door.codePad,
        borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.white, marginTop: -door.codeRise}}>
        <QRCode value={codeUrl} size={code - 2 * door.codePad}/>
      </View>}
    </View>
  </Focusable>;
}

function tvDoorCode(short = false) {
  return short ? wide.door.short.code : wide.door.code;
}

/** Enter the invitation (connect 36): the fields and the keyboard under them at the left, where the code is beside them. */
export function TVInvitationForm({region, fields, keyboard, actions, help}: {region: Region; fields: ReactNode; keyboard: ReactNode; actions: ReactNode;
  help: Readonly<{title: string; paragraphs: readonly string[]}>}) {
  const theme = useTheme(), form = wide.form;
  return <View style={{flexDirection: 'row', gap: form.helpGap, alignItems: 'flex-start'}}>
    <FocusRegion {...region} style={{width: form.width, gap: tvAccountSpec.fieldGap}}>
      {fields}
      {keyboard}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: tvAccountSpec.actionGap}}>{actions}</View>
    </FocusRegion>
    <View style={{flex: 1, gap: theme.space.md}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{help.title}</Text>
      {help.paragraphs.map(text => <Text key={text} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{text}</Text>)}
    </View>
  </View>;
}

/** The servers that aren't answering (connect 37): one list, each with its own Try again. */
export function TVServerList({region, children}: {region: Region; children: ReactNode}) {
  return <FocusRegion {...region} orientation="vertical" style={{width: wide.list.width, gap: wide.list.gap}}>{children}</FocusRegion>;
}

/** "Checking again in 40 seconds" with its ring, and the line of help under it (connect 37). */
export function TVRetryNote({countdown, progress, help}: {countdown?: string; progress?: number; help: string}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.md, marginTop: theme.space.lg, maxWidth: wide.list.width}}>
    {countdown !== undefined && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.md}}>
      <CountdownRing progress={progress}/>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{countdown}</Text>
    </View>}
    <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{help}</Text>
  </View>;
}
