import {useEffect, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {SettingsDocumentAPI} from '@core/settings-document.ts';
import {readBackgroundPause, setBackgroundPause} from '@core/server-admin/general-settings.ts';
import {Button} from '../../../kit/controls';

/** The running pause is read on opening and on Retry; commands are never deferred. */
export function GeneralPause({api, isCurrent, reportError, label}: {api: SettingsDocumentAPI; isCurrent: () => boolean; reportError: (error: unknown) => void; label: string}) {
 const [state, setState] = useState<{ready: boolean; until?: string; failed?: boolean}>({ready: false});
 const [busy, setBusy] = useState(false), [retry, setRetry] = useState(0);
 useEffect(() => {
  const life = new AbortController();
  void readBackgroundPause(api, life.signal).then(until => {if (!life.signal.aborted && isCurrent()) setState({ready: true, until});}, () => {if (!life.signal.aborted && isCurrent()) setState({ready: false, failed: true});});
  return () => life.abort();
 }, [api, isCurrent, retry]);
 useEffect(() => {
  const delay = state.until ? Date.parse(state.until) - Date.now() : 0;
  if (!(delay > 0)) return;
  const timer = setTimeout(() => {if (isCurrent()) setState(before => ({...before, until: undefined}));}, delay);
  return () => clearTimeout(timer);
 }, [state.until, isCurrent]);
 const paused = !!state.until && Date.parse(state.until) > Date.now();
 return <div>
  <Button size="small" variant="glass" loading={busy} disabled={!state.ready || busy} onClick={() => {
   if (!isCurrent() || busy) return;
   setBusy(true);
   void setBackgroundPause(api, !paused).then(until => {if (isCurrent()) setState({ready: true, until});}, error => {if (isCurrent()) {setState(before => ({...before, failed: true}));reportError(error);}}).finally(() => {if (isCurrent()) setBusy(false);});
  }}>{paused ? defaultI18n.t('settings.server.resumeBackground') : label}</Button>
  {paused && <p role="status">{defaultI18n.t('settings.server.backgroundPausedUntil', {time: defaultI18n.time(state.until!)})}</p>}
  {state.failed && <><p role="alert">{defaultI18n.t('next.states.pageFailedBody')}</p>{!state.ready && <Button size="small" variant="glass" onClick={() => setRetry(value => value + 1)}>{defaultI18n.t('next.states.tryAgain')}</Button>}</>}
 </div>;
}
