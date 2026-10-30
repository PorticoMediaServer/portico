import {useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import {BrowserProfileRecovery} from '../../../platform/profile-recovery';
import {AuthCodeBoxes, AuthError, AuthPasswordField} from '../../../kit/accounts/AuthStage';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import type {ProfileSettingsSource} from './source';
import styles from './profile.module.css';
const t = defaultI18n.t;

/** Local-account recovery uses the same temporary sign-in controller as the
 * picker. Its grant never becomes the installed viewer or a profile switch. */
export function ProfileForgotPIN({source, api, selectedFamilyId, isCurrent}: {source: ProfileSettingsSource; api: HttpLocalApi; selectedFamilyId: () => string; isCurrent: () => boolean}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  type Opening = {source: ProfileSettingsSource; flow: BrowserProfileRecovery; current: () => boolean; retire: () => void; profileName: string; username: string};
  const [opening, setOpening] = useState<Opening>();
  const active = useRef<Opening | undefined>(undefined), trigger = useRef<HTMLButtonElement>(null), rendered = useRef(source);
  rendered.current = source;
  useLayoutEffect(() => () => {
    const captured = active.current;
    if (captured?.source === source) {active.current = undefined; captured.retire();}
  }, [source]);
  const selected = source.selected(), account = state.document;
  const close = (captured: Opening) => {
    if (active.current !== captured) return;
    active.current = undefined;captured.retire();setOpening(undefined);
  };
  if (!isCurrent() || !account || account.canManage || !selected?.pinRequired) return null;
  return <>
    <Button ref={trigger} size="small" variant="glass" disabled={state.busy || !!active.current} onClick={() => {
      const document = source.getSnapshot().document, profile = source.selected();
      if (!isCurrent() || active.current || !document || document.canManage || !profile?.pinRequired) return;
      const accountId = document.account.id, accountRevision = document.account.revision;
      const profileId = profile.id, profileRevision = profile.revision;
      let retired = false, off = () => {};
      const current = () => !retired && rendered.current === source && isCurrent() && source.getSnapshot().document === document &&
        document.account.id === accountId && document.account.revision === accountRevision &&
        source.selected() === profile && profile.id === profileId && profile.revision === profileRevision;
      const flow = new BrowserProfileRecovery(api, document, profileId, 'local', selectedFamilyId(), current);
      const captured: Opening = {source, flow, current, profileName: profile.name, username: document.account.username,
        retire: () => {if (retired) return;retired = true;off();flow.dispose();}};
      active.current = captured;
      off = source.subscribe(() => {
        if (current()) return;
        captured.retire();
        if (active.current === captured) {active.current = undefined;setOpening(undefined);}
      });
      if (!current()) {captured.retire();active.current = undefined;return;}
      setOpening(captured);
    }}>{t('next.settingsSpec.account.profile.groups.2.rows.1.control.label')}</Button>
    {opening && <RecoveryForm key={opening.flow.profileId} flow={opening.flow} current={() => active.current === opening && opening.current()}
      profileName={opening.profileName} username={opening.username} close={() => close(opening)} returnFocusRef={trigger} />}
  </>;
}

function RecoveryForm({flow, current, profileName, username, close, returnFocusRef}: {
  flow: BrowserProfileRecovery; current: () => boolean; profileName: string; username: string; close: () => void;
  returnFocusRef: RefObject<HTMLElement | null>;
}) {
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot, flow.getSnapshot);
  const [password, setPassword] = useState(''), [pin, setPIN] = useState(''), [code, setCode] = useState('');
  const live = useRef(true), passwordInput = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {live.current = true;return () => {live.current = false;};}, []);
  const admitted = () => live.current && current();
  const dismiss = () => {setPassword('');setPIN('');setCode('');close();};
  const title = state.phase === 'done' ? t('profile.pinChanged') : state.phase === 'pin' ? t('profile.pinRecoveryTitle', {name: profileName}) : t('accounts.profiles.forgotTitle', {profile: profileName});
  if (!current()) return null;
  return <Dialog open width={420} title={title} returnFocusRef={returnFocusRef} initialFocusRef={state.phase === 'password' ? passwordInput : undefined}
    onOpenChange={open => {if (!open) dismiss();}}>
    <div className={styles.fields}>
      <AuthError error={state.error} />
      {state.phase === 'password' && <form className={styles.fields} onSubmit={event => {
        event.preventDefault();if (!admitted() || state.busy || !password) return;const value = password;setPassword('');void flow.password(value);
      }}>
        <AuthPasswordField ref={passwordInput} label={t('accounts.profiles.accountPassword', {username})} autoComplete="current-password" value={password} disabled={state.busy} onChange={event => setPassword(event.currentTarget.value)} />
        <Button size="small" variant="primary" type="submit" loading={state.busy} disabled={!password}>{t('accounts.signIn.continue')}</Button>
      </form>}
      {state.phase === 'challenge' && <><AuthCodeBoxes length={6} value={code} label={t('profile.pinRecoveryCode')} disabled={state.busy} onChange={setCode} onComplete={value => {if (admitted() && !state.busy) {setCode('');void flow.code(value);}}} /></>}
      {state.phase === 'pin' && <><AuthCodeBoxes length={4} value={pin} label={t('profile.pinChoose')} disabled={state.busy} onChange={setPIN} onComplete={setPIN} />
        <Button size="small" variant="primary" loading={state.busy} disabled={pin.length !== 4} onClick={() => {if (admitted() && !state.busy) {const value = pin;setPIN('');void flow.reset(value);}}}>{t('profile.pinRecoveryAction')}</Button></>}
      {state.phase === 'done' && <p className={styles.limit}>{t('profile.pinSavedBody', {name: profileName})}</p>}
      <Button size="small" onClick={dismiss}>{t(state.phase === 'done' ? 'action.done' : 'action.cancel')}</Button>
    </div>
  </Dialog>;
}
