import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {playbackControlsService, type RemotePlaybackControls} from '@core/playback-controls.ts';
import {Button} from '../../kit/controls';

const t = defaultI18n.t;

/** account-device group 2: what this profile is playing on its other devices, from the
 * server's own account-scoped control list. The only account-scoped control the contract
 * offers is stop, which ends that device's session; the button says Stop, never Pause. */
export function RemoteNowPlaying({api, isCurrent, onFailure}: {
  api: Readonly<{request<T>(path: string, method?: string, body?: unknown): Promise<T>}>;
  isCurrent(): boolean;
  onFailure(error: unknown): void;
}) {
  const [items, setItems] = useState<readonly RemotePlaybackControls[]>([]), [busy, setBusy] = useState<string | undefined>(undefined);
  const client = useRef(playbackControlsService(api.request)).current;
  const load = () => {
    client.remote().then(result => { if (isCurrent()) setItems(result.items); }).catch(error => { if (isCurrent()) onFailure(error); });
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({load});
  useEffect(() => {setupPorts.current = {load};});
  useEffect(() => {
    const {load} = setupPorts.current;
    load();
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
    // A retired authority must stop reading the account's other sessions.
  }, [client]);
  const pause = (entry: RemotePlaybackControls) => {
    setBusy(entry.sessionId);
    client.stopAccount(entry).then(() => { if (isCurrent()) setItems(current => current.filter(item => item.sessionId !== entry.sessionId)); })
      .catch(error => { if (isCurrent()) onFailure(error); }).finally(() => { if (isCurrent()) setBusy(undefined); });
  };
  if (!items.length) return null;
  return <ul style={{display: 'flex', flexDirection: 'column', gap: 8, margin: 0, padding: 0, listStyle: 'none'}} aria-label={t('next.settingsSpec.account.device.groups.2.rows.0.label')}>
    {items.map(entry => <li key={entry.sessionId} style={{display: 'flex', alignItems: 'center', gap: 12}}>
      <span style={{minWidth: 0, flex: 1}}><b>{entry.device.name}</b>{entry.item && <small style={{display: 'block'}}>{entry.item.title}</small>}</span>
      <Button size="small" variant="glass" disabled={busy === entry.sessionId || !entry.actions.includes('stop')} onClick={() => pause(entry)}>
        {t('next.dock.stop')}</Button>
    </li>)}
  </ul>;
}
