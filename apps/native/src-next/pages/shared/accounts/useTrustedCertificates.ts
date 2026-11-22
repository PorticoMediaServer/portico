import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import type {View} from 'react-native';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {SavedCertificate} from '../../../platform/certificate-trust';
import {forgetCapturedCertificate, savedCertificates} from '../../../platform/server-connections';

type Ticket = {context?: NativeShellBootstrapContext; active: boolean; flight: boolean; read: number};
type Opening = {ticket: Ticket; certificate: SavedCertificate};

/** The device's trusted certificates: actual native records, independent of the selected server session. One read per
 * opening; Forget asks first and acts only on the exact certificate it was drawn for, once. Moved from the old screen. */
export function useTrustedCertificates(context?: NativeShellBootstrapContext) {
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
  const ask = (value: SavedCertificate) => {
    if (!current() || ticket.flight) return;
    const next = {ticket, certificate: Object.freeze({...value})}; opening.current = next; setShown(next);
  };
  return {rows, error, busy, cancelFocus, captured: captured?.certificate, ask, close, confirm: () => {void confirm();}, refresh: () => {void refresh();}};
}
