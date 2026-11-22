import {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Linking} from 'react-native';
import {readServerSystem} from '../../../../../../packages/client-core/src/query/connection-operations.ts';
import {parseServerSignIn} from '../../../../../../packages/client-core/src/sign-in';
import {sendNativeDeviceReport} from '../../../screens/settings-live/device-client-report';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panel-deps';
import type {NativeShellRuntime} from '../../../shell/runtime';

type Props = NativeSettingsPanelDeps & {runtime: NativeShellRuntime};
export const deviceHelpAddress = 'getportico.tv/help';

export function useDeviceAbout({context, lifetime, runtime, openRoute}: Props) {
  const [serverVersion, setServerVersion] = useState<string>(), [serverApi, setServerApi] = useState<string>(), [versionError, setVersionError] = useState<unknown>(), [retry, setRetry] = useState(0);
  useEffect(() => {
    const caller = new AbortController();
    setServerVersion(undefined); setServerApi(undefined); setVersionError(undefined);
    void lifetime.request(signal => readServerSystem(context.api, signal).then(parseServerSignIn), caller.signal).then(system => {
      if (caller.signal.aborted || !lifetime.current()) return;
      if (system.id !== lifetime.authority.serverId || typeof system.version !== 'string' || !system.version.trim()) throw new Error('native_settings_server_version_invalid');
      setServerVersion(system.version);
      // O-159: the server's API version names both sides of any report (absent on a pre-release server).
      const api = (system as {apiVersion?: unknown}).apiVersion;
      setServerApi(typeof api === 'string' && /^\d{1,6}\.\d{1,6}$/.test(api) ? api : undefined);
    }).catch(error => {if (!caller.signal.aborted && lifetime.current()) setVersionError(error);});
    return () => caller.abort();
  }, [context, lifetime, retry]);
  const help = () => {
    lifetime.assert();
    void Linking.openURL(`https://${deviceHelpAddress}`).catch(error => {if (lifetime.current()) runtime.reportError(error);});
  };
  const openLicences = () => {lifetime.assert(); openRoute({kind: 'licenses'});};
  return {serverVersion, serverApi, versionError, retry: () => setRetry(value => value + 1), help, openLicences};
}

export function useDeviceReport(props: Props) {
  const {context, lifetime} = props;
  type Ticket = {context: typeof context; lifetime: typeof lifetime; controller: AbortController};
  const [sent, setSent] = useState(false), [ticket, setTicket] = useState<Ticket>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const active = useRef<Ticket | undefined>(undefined), pending = useRef<Ticket | undefined>(undefined);
  const owner = useRef({context, lifetime});
  const invalidate = () => {const held = active.current; active.current = undefined; held?.controller.abort();};
  if (owner.current.context !== context || owner.current.lifetime !== lifetime) {
    invalidate(); owner.current = {context, lifetime};
  }
  useLayoutEffect(() => () => {invalidate();}, [context, lifetime]);
  const current = (asked: Ticket | undefined): asked is Ticket => !!asked && active.current === asked && !asked.controller.signal.aborted
    && owner.current.context === asked.context && owner.current.lifetime === asked.lifetime && asked.lifetime.current();
  const close = () => {
    if (!current(ticket)) return;
    invalidate(); setTicket(undefined); setBusy(false); setError(undefined);
  };
  const send = () => {
    if (!current(ticket) || pending.current) return;
    const asked = ticket;
    pending.current = asked; setBusy(true); setError(undefined); setSent(false);
    void sendNativeDeviceReport(props, {signal: asked.controller.signal, current: () => current(asked)}).then(() => {
      if (!current(asked)) return;
      invalidate(); setTicket(undefined); setBusy(false); setSent(true);
    }, failure => {if (current(asked)) setError(failure);}).finally(() => {
      if (pending.current === asked) pending.current = undefined;
      if (current(asked)) setBusy(false);
    });
  };
  const open = () => {
    lifetime.assert();
    invalidate();
    const next = {context, lifetime, controller: new AbortController()};
    active.current = next; setTicket(next); setBusy(false); setError(undefined); setSent(false);
  };
  return {sent, open, active: current(ticket), close, busy, error, send};
}
