import React, {useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {FormScrollView} from '../../platform/keyboard';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {parseJoinLink} from '../../../../../packages/client-core/src/join-link';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {ColourBackdrop} from '../../kit/artwork';
import {Button, TextField} from '../../kit/controls';
import {nativeType, useTheme} from '../../kit/theme';
import {qrScannerAvailable, scanQRCode} from '../../platform/qr-scanner';

const alwaysCurrent = () => true;
const ownerCurrent = (current: () => boolean) => {try {return current();} catch {return false;}};

/** Empty directory only: discovery and invitation entry still prove their server. */
export function PhoneNoServers({busy, error, onFind, onRefresh, onShare, onSignOut, onBack, onJoin, onSettings, onDownloads, username, isCurrent = alwaysCurrent, notice}: {
  busy: boolean; error?: React.ReactNode; notice?: Readonly<{title: string; body: string}>; isCurrent?: () => boolean; onSettings?: () => void; onDownloads?: () => void; username?: string; onFind: () => void; onRefresh: () => void;
  onJoin: (invitation: string, address: string) => void; onShare: () => void; onSignOut: () => void; onBack?: () => void;
}) {
  const [invitation, setInvitation] = useState(''), [address, setAddress] = useState('');
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const link = parseJoinLink(invitation);
  const [, redraw] = useState(0);
  const opening = useRef<{active: boolean; invitation: string; address: string; owner: () => boolean; busy: boolean; scan?: AbortController; failed: boolean} | undefined>(undefined);
  const latestBusy = useRef(busy); latestBusy.current = busy;
  if (!opening.current || !opening.current.active || opening.current.invitation !== invitation || opening.current.address !== address || !ownerCurrent(opening.current.owner) || opening.current.busy !== busy) {
    if (opening.current) {opening.current.active = false; opening.current.scan?.abort();}
    opening.current = {active: true, invitation, address, owner: isCurrent, busy, failed: false};
  }
  const ticket = opening.current;
  useLayoutEffect(() => {if (opening.current === ticket) ticket.active = true; return () => {ticket.active = false; ticket.scan?.abort();};}, [ticket]);
  const current = () => ticket.active && opening.current === ticket && !latestBusy.current && ownerCurrent(ticket.owner);
  const changeAddress = (raw: string) => {if (!current() || raw === ticket.address) return; ticket.active = false; ticket.scan?.abort(); setAddress(raw);};
  const changeInvitation = (raw: string) => {if (!current() || raw === ticket.invitation) return; ticket.active = false; ticket.scan?.abort(); setInvitation(raw);
    const target = parseJoinLink(raw); if (target) setAddress(target.server);};
  const join = () => {if (!current() || ticket.scan || !ticket.invitation.trim()) return; ticket.active = false; onJoin(ticket.invitation, ticket.address);};
  const scan = () => {
    if (!current() || ticket.scan || !qrScannerAvailable()) return;
    const controller = new AbortController(); ticket.scan = controller; ticket.failed = false; redraw(value => value + 1);
    void scanQRCode({signal: controller.signal, isCurrent: () => current() && ticket.scan === controller,
      title: t('accounts.move.scanQR'), cancelLabel: t('action.cancel')}).then(raw => {
      if (!current() || ticket.scan !== controller || raw === undefined) return;
      // Scanning edits the form only. Canonical proof/preview still requires Continue.
      ticket.active = false; controller.abort(); setInvitation(raw);
      const target = parseJoinLink(raw); if (target) setAddress(target.server);
      redraw(value => value + 1);
    }, failure => {
      if (current() && ticket.scan === controller && failure?.code !== 'qr_cancelled') ticket.failed = true;
    }).finally(() => {
      if (ticket.scan !== controller) return; ticket.scan = undefined;
      if (current()) redraw(value => value + 1);
    });
  };
  return <View testID="phone-no-servers" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <FormScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingHorizontal: 24, paddingTop: insets.top + 24, paddingBottom: Math.max(24, insets.bottom + 16), gap: 20}}>
      {onBack && <Button variant="text" disabled={busy} onPress={onBack} style={{alignSelf: 'flex-start'}}>{t('action.back')}</Button>}
      <View style={{gap: 12}}>
        <Text accessibilityRole="header" style={[nativeType({size: 30, lineHeight: 34, weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.text}]}>{notice?.title ?? t('web.hostedAccount.noServers')}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{notice?.body ?? t('web.chooser.noneBody')}</Text>
        {error}
      </View>
      {onDownloads && <Button variant="glass" disabled={busy} onPress={onDownloads}>{t('downloads.title')}</Button>}
      {onSettings && <Button variant="text" disabled={busy} onPress={onSettings}>{username ? username + " · " + t('settings.title') : t('settings.title')}</Button>}
      <View style={{gap: 12, padding: 16, borderRadius: 16, backgroundColor: theme.colors.raised}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('web.directJoin.title')}</Text>
        <TextField label={t('accounts.signIn.address')} value={link?.server ?? address} onChangeText={changeAddress} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy && !link}/>
        <TextField label={t('services.start.invitation')} placeholder={t('web.directJoin.codePlaceholder')} value={invitation} onChangeText={changeInvitation} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.directJoin.entryBody')}</Text>
        {qrScannerAvailable() && <Button variant="glass" disabled={busy || !!ticket.scan} loading={!!ticket.scan} onPress={scan}>{t('accounts.move.scanQR')}</Button>}
        {ticket.failed && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{t('error.unknown')}</Text>}
        <Button disabled={busy || !invitation.trim()} onPress={join}>{t('action.continue')}</Button>
      </View>
      <View style={{gap: 10}}>
        <Button variant="glass" disabled={busy} onPress={onFind}>{t('connection.discovery.find')}</Button>
        <Button variant="text" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
      </View>
      <View style={{gap: 12, padding: 16, borderRadius: 16, backgroundColor: theme.colors.raised}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('services.start.setup')}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('services.start.phoneSetupBody')}</Text>
        <Button variant="glass" disabled={busy} onPress={onShare}>{t('services.start.shareLink')}</Button>
      </View>
      <Button variant="text" disabled={busy} onPress={onSignOut}>{t('accounts.signOut.portico')}</Button>
    </FormScrollView>
  </View>;
}
