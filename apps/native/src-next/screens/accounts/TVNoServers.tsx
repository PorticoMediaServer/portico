import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, ScrollView, Text, View, useWindowDimensions} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {NativeTVKeyboard} from '../search/TVKeyboard';
import {ColourBackdrop} from '../../kit/artwork';
import {Button, Focusable, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {shellLayout} from '../../shell/model';
import {useTVBusyEntryRevision} from '../../tv/auth/entry-revision';

type Props = {busy: boolean; error?: React.ReactNode; isCurrent: () => boolean; onBack: () => void;
  onJoin: (invitation: string, address: string) => void; onFind: () => void; onRefresh: () => void; onSignOut: () => void; notice?: {title: string; body: string; onRetry?: () => void}};
// Frame 36 draws the Portico keyboard nine across at the connect screen, wider than
// Search's six; the code's last digit sits on the action row ahead of Space.
const addressKeys = ['abcdefghi', 'jklmnopqr', 'stuvwxyz.', '012345678', '9:/@-_'].map(row => [...row]);
const invitationKeys = ['ABCDEFGHI', 'JKLMNOPQR', 'STUVWXYZ-', '123456789'].map(row => [...row]);
type Ticket = {live: boolean; scope: object};

/** Frames 35 and 38 draw two native focus stops. Setup is informational:
 * OK keeps its existing QR/steps focused and invokes no command. */
function DoorTiles({busy, onJoin}: {busy: boolean; onJoin: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  // Entry is once per actual mount of these tiles: no busy ticket, so a busy turn that has nothing
  // to do with them cannot advance the entry and put focus back on Join.
  useDestinationEntryFocus({visible: true, intent: 'enter', revision: 0}, {regionId: 'accounts.empty.door', childId: 'join'});
  const tile = {flex: 1, minHeight: 440, paddingVertical: 44, paddingHorizontal: 48, borderRadius: 28,
    backgroundColor: 'rgba(13, 27, 37, 0.78)', borderWidth: 1.5, borderColor: 'rgba(237, 245, 250, 0.10)'} as const;
  const heading = [nativeType({size: 44, lineHeight: 46.2, weight: 800, tracking: -.035}, theme.textScale), {color: theme.colors.text, marginBottom: 14}];
  const body = [nativeType({size: 26, lineHeight: 37.7, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}];
  const chip = (icon: 'i-link' | 'i-server') => <View style={{width: 76, height: 76, borderRadius: 38, alignItems: 'center', justifyContent: 'center',
    marginBottom: 26, backgroundColor: `${theme.colors.accent}29`}}><Icon name={icon} size={34} color={theme.colors.accentStrong}/></View>;
  // The guides stay through busy: the controls remain real native stops, read-only while busy.
  return <FocusRegion id="accounts.empty.door" entry="join" order={['join', 'setup']} orientation="horizontal" style={{flexDirection: 'row', gap: 40, alignItems: 'stretch'}}>
    <Focusable focusId="join" focusKind="card" focusRadius={28} disabled={busy} focusableWhenDisabled onPress={onJoin} accessibilityRole="button"
      style={{...tile, boxShadow: [{offsetX: 0, offsetY: 60, blurRadius: 120, spreadDistance: -40, color: 'rgba(0, 0, 0, 0.9)'}]}}>
      {chip('i-link')}
      <Text style={heading}>{t('services.start.join')}</Text>
      <Text style={body}>{t('services.start.joinBody')}</Text>
    </Focusable>
    <Focusable focusId="setup" focusKind="card" focusRadius={28} disabled={busy} focusableWhenDisabled accessibilityRole="button" accessibilityLabel={t('services.start.setup')} style={tile}>
      {chip('i-server')}
      <Text style={heading}>{t('services.start.setup')}</Text>
      <Text style={[...body, {maxWidth: 380}]}>{t('services.start.phoneSetupBody')}</Text>
      <View accessible accessibilityLabel="https://getportico.tv/download/" style={{position: 'absolute', right: 44, top: 190, width: 220, height: 220, padding: 16, borderRadius: 18, backgroundColor: theme.colors.white}}>
        <QRCode value="https://getportico.tv/download/" size={188}/>
      </View>
    </Focusable>
  </FocusRegion>;
}

/** Empty authenticated directory only. Joining still uses the parent's original
 * invitation parser, issuer inspection and normal account continuation. */
export function TVNoServers(props: Props) {
  const {isCurrent, onBack} = props;
  const theme = useTheme(), t = defaultI18n.t;
  const [joining, setJoining] = useState(false), [address, setAddress] = useState(''), [invitation, setInvitation] = useState('');
  const [field, setField] = useState<'address' | 'invitation'>('address');
  const entryRevision = useTVBusyEntryRevision(props.busy);
  const draft = useMemo(() => Object.freeze({joining, address, invitation, field}), [joining, address, invitation, field]);
  const latest = useRef(draft); latest.current = draft;
  const scope = useMemo(() => ({isCurrent}), [isCurrent]);
  const active = useRef<Ticket | undefined>(undefined), [committed, setCommitted] = useState<Ticket>();
  if (active.current && active.current.scope !== scope) {active.current.live = false; active.current = undefined;}
  useLayoutEffect(() => {const ticket = {live: true, scope}; active.current = ticket; setCommitted(ticket);
    return () => {ticket.live = false; if (active.current === ticket) active.current = undefined;};}, [scope]);
  const current = useCallback(() => !!committed?.live && committed.scope === scope && active.current === committed && isCurrent(), [committed, scope, isCurrent]);
  const owns = () => current() && latest.current === draft && !props.busy;
  const back = useCallback(() => {if (!current()) return; if (joining) {setInvitation(''); setJoining(false);} else onBack();}, [current, joining, onBack]);
  useEffect(() => {const subscription = BackHandler.addEventListener('hardwareBackPress', () => {if (!joining || !current()) return false; back(); return true;});
    return () => subscription.remove();}, [joining, current, back]);
  const window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, true);
  // Frame 36 draws the keyboard at 604; the address field's five key rows make one row more
  // than the invitation's, so the actions follow the active keyboard instead of a fixed slot.
  const keyRows = (field === 'address' ? addressKeys : invitationKeys).length + 1;
  const joinTop = 604 + keyRows * 72 + (keyRows - 1) * 10 + 16;
  const footerTop = joinTop + 92;
  const noticeView = props.notice && <View accessibilityRole="alert" style={{gap: 12, padding: 20, borderRadius: 16, backgroundColor: theme.colors.raised}}>
    <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{props.notice.title}</Text>
    <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{props.notice.body}</Text>
    {props.notice.onRetry && <Button variant="text" disabled={props.busy} onPress={() => {if (current()) props.notice?.onRetry?.();}}>{t('web.chooser.tryAgain')}</Button>}
  </View>;
  return <FocusProvider><View testID="tv-no-servers" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    {joining ? <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      {/* Frame 36's 1920x1080 canvas: the header at 96, the address and code fields at 330 and 470,
        the 860 keyboard at 604 and the code help at 1160/300. The frame leaves Join, Find and Sign out
        to the remote, so they sit below 1080 and this canvas scrolls rather than clipping them. */}
      <ScrollView keyboardShouldPersistTaps="handled" style={{width: canvas.width, height: canvas.height}}
        contentContainerStyle={{width: canvas.width, minHeight: footerTop + 128, paddingBottom: 64}}>
        <Button variant="text" disabled={props.busy} onPress={back} style={{position: 'absolute', left: 200, top: 28}}>{t('action.back')}</Button>
        <View style={{position: 'absolute', left: 200, top: 96, width: 1300, gap: 12}}>
          <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.accentStrong}]}>{t('services.start.ready')}</Text>
          <Text accessibilityRole="header" style={[nativeType({size: 72, lineHeight: 72, weight: 800, tracking: -.045}, theme.textScale), {color: theme.colors.text}]}>{t('accounts.invitation.tvTitle')}</Text>
          <Text style={[nativeType({size: 26, lineHeight: 37.7, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, maxWidth: 1000}]}>{t('accounts.invitation.tvLede')}</Text>
          {noticeView}
          {props.error}
        </View>
        <FocusRegion id="accounts.empty.fields" entry="address" order={['address', 'invitation', 'join', 'find', 'sign-out']} style={{position: 'absolute', left: 0, top: 0, width: canvas.width, height: 1220}}>
          <View style={{position: 'absolute', left: 200, top: 296, width: 860}}>
            <TextField focusId="address" label={t('accounts.signIn.address')} value={address} onFocus={() => {if (current()) setField('address');}} showSoftInputOnFocus={false} editable={!props.busy} autoCapitalize="none" autoCorrect={false}
              onChangeText={value => {if (owns()) setAddress(value);}}/>
          </View>
          <View style={{position: 'absolute', left: 200, top: 436, width: 860}}>
            <TextField focusId="invitation" label={t('services.start.invitation')} value={invitation} onFocus={() => {if (current()) setField('invitation');}} showSoftInputOnFocus={false} editable={!props.busy} autoCapitalize="none" autoCorrect={false} autoComplete="off"
              onChangeText={value => {if (owns()) setInvitation(value);}}/>
          </View>
          <View style={{position: 'absolute', left: 200, top: 604, width: 860}}>
            <NativeTVKeyboard id={`accounts.empty.keyboard.${field}`} value={field === 'address' ? address : invitation}
              rows={field === 'address' ? addressKeys : invitationKeys} columns={9} width={860} lead={field === 'invitation' ? '0' : undefined} leftRegionId="accounts.empty.fields"
              // The typed field owns the odd ticket; a completed pending run moves both kinds on.
              activation={{visible: current() && !props.busy, intent: 'enter', revision: entryRevision * 2 + (field === 'address' ? 0 : 1)}}
              onChangeText={value => {if (owns()) {if (field === 'address') setAddress(value); else setInvitation(value);}}}/>
          </View>
          <Button focusId="join" disabled={props.busy || !invitation.trim()} onPress={() => {if (owns()) {setInvitation(''); props.onJoin(draft.invitation, draft.address);}}} style={{position: 'absolute', left: 200, top: joinTop, width: 320}}>{t('services.start.joinAction')}</Button>
          <View style={{position: 'absolute', left: 200, top: footerTop, flexDirection: 'row', gap: 24}}>
            <Button focusId="find" variant="text" disabled={props.busy} onPress={() => {if (current()) props.onFind();}}>{t('connection.discovery.find')}</Button>
            <Button focusId="sign-out" variant="text" disabled={props.busy} onPress={() => {if (current()) props.onSignOut();}}>{t('accounts.signOut.portico')}</Button>
          </View>
        </FocusRegion>
        <View style={{position: 'absolute', left: 1160, top: 300, width: 664, gap: 18}}>
          <Text accessibilityRole="header" style={[nativeType({size: 36, lineHeight: 'normal', weight: 800, tracking: -.03}, theme.textScale), {color: theme.colors.text}]}>{t('accounts.invitation.tvCodeHelpTitle')}</Text>
          <Text style={[nativeType({size: 24, lineHeight: 36, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('accounts.invitation.tvCodeHelpBody')}</Text>
          <Text style={[nativeType({size: 24, lineHeight: 36, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('accounts.invitation.tvCodeHelpPhone')}</Text>
        </View>
      </ScrollView>
    </View> : <View style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
      transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>
      {/* Frame 35 puts the header at 96 in the gutter's 1300 column and the tiles at 400;
        frame 38 keeps the same tiles at 460 under its notice at 330. */}
      <View style={{position: 'absolute', left: 200, top: 96, width: 1300}}>
        <Text style={[nativeType({size: 24, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.accentStrong, marginBottom: 12}]}>{t('services.start.ready')}</Text>
        <Text accessibilityRole="header" style={[nativeType({size: 72, lineHeight: 72, weight: 800, tracking: -.045}, theme.textScale), {color: theme.colors.text, marginBottom: 16}]}>{t('services.start.welcome')}</Text>
        <Text style={[nativeType({size: 26, lineHeight: 37.7, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textSecondary, maxWidth: 1000}]}>{t('services.start.body')}</Text>
      </View>
      {(props.notice || props.error) && <View style={{position: 'absolute', left: 200, right: 96, top: 330, gap: 16}}>
        {noticeView}
        {props.error}
      </View>}
      <View style={{position: 'absolute', left: 200, right: 96, top: props.notice || props.error ? 460 : 400}}>
        <DoorTiles busy={props.busy} onJoin={() => {if (owns()) setJoining(true);}}/>
      </View>
      <View style={{position: 'absolute', left: 200, bottom: 64, flexDirection: 'row', gap: 36}}>
        <Button variant="text" disabled={props.busy} onPress={() => {if (current()) props.onFind();}}>{t('connection.discovery.find')}</Button>
        <Button variant="text" disabled={props.busy} onPress={() => {if (current()) props.onSignOut();}}>{t('accounts.signOut.portico')}</Button>
      </View>
    </View>}
  </View></FocusProvider>;
}
