import React, {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {SavedCertificate} from '../../platform/certificate-trust';
import {forgetCapturedCertificate, savedCertificates} from '../../platform/server-connections';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {useTheme} from '../../kit/theme';

type Ticket = {context?: NativeShellBootstrapContext; active: boolean; flight: boolean; read: number};
type Opening = {ticket: Ticket; certificate: SavedCertificate};

/** Device pins are actual native records, independent of the selected server session. */
export function TVTrustedCertificates({context}: {context?: NativeShellBootstrapContext}) {
  const theme = useTheme(), t = defaultI18n.t;
  const owner = useRef<Ticket | undefined>(undefined), opening = useRef<Opening | undefined>(undefined);
  if (!owner.current || owner.current.context !== context) {
    if (owner.current) owner.current.active = false;
    opening.current = undefined;
    owner.current = {context, active: true, flight: false, read: 0};
  }
  const ticket = owner.current;
  const [rows, setRows] = useState<readonly SavedCertificate[]>([]), [shown, setShown] = useState<Opening>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const cancelFocus = useRef<View | null>(null);
  const current = useCallback(() => ticket.active && owner.current === ticket && (!ticket.context || ticket.context.current()), [ticket]);
  const refresh = useCallback(async () => {
    if (!current()) return;
    const read = ++ticket.read;
    try {const value = await savedCertificates(); if (current() && ticket.read === read) {setRows(value); setError(undefined);}}
    catch (failure) {if (current() && ticket.read === read) setError(failure);}
  }, [current, ticket]);
  useLayoutEffect(() => {
    ticket.active = true; setRows([]); setShown(undefined); setError(undefined); setBusy(false); void refresh();
    return () => {ticket.active = false; ++ticket.read; if (opening.current?.ticket === ticket) opening.current = undefined;};
  }, [refresh, ticket]);
  const captured = shown?.ticket === ticket ? shown : undefined;
  const close = () => {if (!captured || opening.current !== captured) return; opening.current = undefined; setShown(undefined);};
  const confirm = async () => {
    if (!captured || opening.current !== captured || !current() || ticket.flight) return;
    ticket.flight = true; setBusy(true); setError(undefined);
    try {
      await forgetCapturedCertificate(captured.certificate);
      if (!current() || opening.current !== captured) return;
      close(); await refresh();
    } catch (failure) {if (current() && opening.current === captured) setError(failure);}
    finally {ticket.flight = false; if (current()) setBusy(false);}
  };
  if (!rows.length && !error) return null;
  return <View testID="tv-trusted-certificates" style={{gap: 12}}>
    <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('connection.certificates.title')}</Text>
    {error != null && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger}]}>{presentError(error, 'account').body}</Text>}
    {rows.map(value => <View key={value.address} style={{gap: 6}}>
      <Text style={[theme.type.body, {color: theme.colors.text}]}>{value.address}</Text>
      <Text selectable style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{value.fingerprint}</Text>
      <Button variant="text" disabled={busy} onPress={() => {
        if (!current() || ticket.flight) return;
        const next = {ticket, certificate: Object.freeze({...value})}; opening.current = next; setShown(next);
      }}>{t('connection.certificates.forget')}</Button>
    </View>)}
    <Button variant="text" disabled={busy} onPress={() => {void refresh();}}>{t('accounts.servers.refresh')}</Button>
    <Dialog testID="tv-trusted-certificate-forget" open={!!captured} onOpenChange={open => {if (!open) close();}} title={t('connection.certificates.confirm')}
      initialFocusRef={cancelFocus} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showClose={!busy}
      footer={<View style={{gap: 12}}>
        <Button focusId="cancel" ref={cancelFocus} variant="glass" disabled={busy} onPress={close}>{t('action.cancel')}</Button>
        <Button focusId="forget" variant="text" disabled={busy} textStyle={{color: theme.colors.danger}} onPress={() => {void confirm();}}>{t('connection.certificates.forget')}</Button>
      </View>}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('connection.certificates.body', {address: captured?.certificate.address ?? ''})}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('connection.certificates.fingerprint')}</Text>
      <Text selectable style={[theme.type.body, {color: theme.colors.text}]}>{captured?.certificate.fingerprint}</Text>
    </Dialog>
  </View>;
}
