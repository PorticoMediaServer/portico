import {useEffect, useRef, useState, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {SettingsRow} from '@core/presentation/settings-spec.ts';
import {Icon} from '../../kit/Icon';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays/Dialog';
import type {SettingPresentation} from '../../kit/settings';
import {isPrivacyAction, privacyActionClaim, privacyAuthFailure, runPrivacyAction, type PrivacyActionApi, type PrivacyActionRowId} from './privacy-actions';
import styles from './privacy.module.css';

const t = defaultI18n.t;

const resultCopy: Readonly<Record<PrivacyActionRowId, MessageId>> = {
  'privacy.clearHistory': 'next.settings.privacy.historyCleared',
  'privacy.clearSearches': 'next.settings.privacy.searchesCleared',
  'privacy.resetRecommendations': 'next.settings.privacy.recommendationsReset',
};
const failureCopy: Readonly<Record<PrivacyActionRowId, MessageId>> = {
  'privacy.clearHistory': 'next.settings.privacy.historyFailed',
  'privacy.clearSearches': 'next.settings.privacy.searchesFailed',
  'privacy.resetRecommendations': 'next.settings.privacy.recommendationsFailed',
};

export type PrivacySurfacesOptions = Readonly<{
  api: PrivacyActionApi;
  serverId: string;
  viewerId: string;
  isCurrent: () => boolean;
  reportError: (error: unknown) => void;
}>;

/** Claims only the three Clear data rows; every other Settings row stays with
 * the shared controller. The actions are offered only while this mounted
 * session is current, and their real producers answer or refuse each call. */
export type PrivacySettingsSurfaces = Readonly<{
  eligible: (row: SettingsRow) => boolean | undefined;
  rowState: (row: SettingsRow) => SettingPresentation | undefined;
  special: (row: SettingsRow, state: SettingPresentation) => ReactNode | undefined;
  dispose: () => void;
}>;

export function createPrivacySettingsSurfaces(options: PrivacySurfacesOptions): PrivacySettingsSurfaces {
  const lifetime = new AbortController();
  const current = () => !lifetime.signal.aborted && options.isCurrent();
  return {
    eligible: row => privacyActionClaim(row.id),
    rowState: row => privacyActionClaim(row.id) ? {present: current()} : undefined,
    special: (row, state) => isPrivacyAction(row.id) ? <PrivacyAction row={row} state={state} {...options} current={current} signal={lifetime.signal} /> : undefined,
    dispose: () => lifetime.abort(),
  };
}

function PrivacyAction({row, state, api, serverId, viewerId, reportError, current, signal}: PrivacySurfacesOptions & {
  row: SettingsRow; state: SettingPresentation; current: () => boolean; signal: AbortSignal;
}) {
  const id = row.id as PrivacyActionRowId;
  const label = t(row.control.label ?? row.label);
  const verb = label.replace(/…$/, '');
  const confirm = row.control.confirm && t(row.control.confirm);
  const [confirming, setConfirming] = useState(false), [busy, setBusy] = useState(false), [result, setResult] = useState<PrivacyActionRowId>(), [failed, setFailed] = useState(false);
  const opener = useRef<HTMLButtonElement>(null), cancel = useRef<HTMLButtonElement>(null), mounted = useRef(true);
  useEffect(() => {mounted.current = true; return () => {mounted.current = false;};}, []);
  const run = async () => {
    if (busy || !current()) return;
    setBusy(true); setFailed(false); setResult(undefined);
    try {
      await runPrivacyAction(id, {api, serverId, viewerId, signal});
      if (mounted.current && current()) {setResult(id); setConfirming(false);}
    } catch (error) {
      if (!mounted.current || !current()) return;
      if (privacyAuthFailure(error)) {reportError(error); return;}
      setFailed(true);
    } finally {
      if (mounted.current && current()) setBusy(false);
    }
  };
  const present = state.present && current();
  return <>
    {result && <span className={styles.result} role="status"><Icon name="i-check" size={16} />{t(resultCopy[result])}</span>}
    {failed && <span className={styles.failure} role="alert">{t(failureCopy[id])}</span>}
    <Button ref={opener} variant="glass" size="xs" disabled={!present} loading={busy}
      onClick={() => {if (confirm) setConfirming(true); else void run();}}>{label}</Button>
    {confirm && <Dialog open={confirming} onOpenChange={open => {if (!busy) setConfirming(open);}} title={`${verb}?`} description={confirm}
      initialFocusRef={cancel} returnFocusRef={opener} footer={<><Button ref={cancel} size="small" disabled={busy} onClick={() => setConfirming(false)}>{t('next.settings.cancel')}</Button><Button variant="primary" size="small" loading={busy} onClick={() => {void run();}}>{verb}</Button></>}>
      <span />
    </Dialog>}
  </>;
}
