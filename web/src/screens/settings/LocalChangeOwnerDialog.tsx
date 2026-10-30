import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {errorMessage} from '@core/presentation/errors.ts';
import {Icon} from '../../kit/Icon';
import {Button, TextField} from '../../kit/controls';
import {LocalOwnerChoiceFields, type LocalOwnerChoiceMode} from './LocalOwnerChoiceFields';
import {Dialog} from '../../kit/overlays/Dialog';
import styles from './local-change-owner.module.css';
import type {LocalOwnerTransferPort} from './local-owner-transfer';

const t = defaultI18n.t;

type Witness = {
  readonly port: LocalOwnerTransferPort;
  readonly onClose: () => void;
  readonly onError?: (error: unknown) => void;
  live: boolean;
  busy: boolean;
  readonly abort: AbortController;
};

/** auth 24h, local kind: the drawn dialog over the Sign-in page. It never calls the
 * transfer itself; the supplied port owns that guarded request. */
export function LocalChangeOwnerDialog({open, port, onClose, onError}: {
  open: boolean;
  port?: LocalOwnerTransferPort;
  onClose: () => void;
  onError?: (error: unknown) => void;
}) {
  const [witness, setWitness] = useState<Witness | null>(null);
  // A same-tick close/reopen can batch false->true; retiring an accepted witness
  // advances this generation so a fresh one is minted for the reopened dialog.
  const [generation, setGeneration] = useState(0);
  const witnessRef = useRef<Witness | null>(null);
  const [selected, setSelected] = useState(''), [mode, setMode] = useState<LocalOwnerChoiceMode>('existing');
  const [username, setUsername] = useState(''), [name, setName] = useState(''), [newPassword, setNewPassword] = useState('');
  const [password, setPassword] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({onError, onClose});
  useLayoutEffect(() => {setupPorts.current = {onError, onClose};});
  useLayoutEffect(() => {
    const {onError, onClose} = setupPorts.current;
    if (!open || !port) return;
    // The callbacks and port belong to this opening; a later prop is never adopted.
    const fresh: Witness = {port, onClose, onError, live: true, busy: false, abort: new AbortController()};
    witnessRef.current = fresh;
    setWitness(fresh);
    // The approved dialog has no default owner: the caller must choose one explicitly.
    setSelected('');setMode('existing');setUsername('');setName('');setNewPassword('');
    setPassword('');
    setError(undefined);
    setBusy(false);
    return () => {
      fresh.live = false;
      if (witnessRef.current === fresh) witnessRef.current = null;
      setWitness(current => current === fresh ? null : current);
      setPassword('');setNewPassword('');
      fresh.abort.abort();
    };
  }, [open, port, generation]);
  const drawn = witness && witness.live && witnessRef.current === witness && open && witness.port === port ? witness : null;
  const retire = (held: Witness) => {
    held.live = false;
    if (witnessRef.current === held) witnessRef.current = null;
    setWitness(current => current === held ? null : current);
    setPassword('');setNewPassword('');setUsername('');setName('');
    setBusy(false);
    setGeneration(value => value + 1);
  };
  const close = () => {
    const held = witnessRef.current;
    if (!held || held !== witness || !held.live || held.busy) return;
    retire(held);
    held.onClose();
  };
  if (!port || !drawn) return null;
  const members = drawn.port.opening.members, accept = drawn.port.accept;
  const live = () => {
    const held = witnessRef.current;
    return !!held && held === drawn && held === witness && held.live && open && held.port === port && !held.abort.signal.aborted;
  };
  const choose = (value: string) => {if (live() && !drawn.busy) setSelected(value);};
  const typePassword = (value: string) => {if (live() && !drawn.busy) setPassword(value);};
  const changeMode = (value: LocalOwnerChoiceMode) => {if (live() && !drawn.busy) {setMode(value);setSelected('');setUsername('');setName('');setNewPassword('');setPassword('');setError(undefined);}};
  const submit = () => {
    const held = witnessRef.current;
    if (!held || held !== witness || held !== drawn || !held.live || held.busy || !password || (mode === 'existing' ? !selected : !username.trim() || !name.trim() || !newPassword)) return;
    // Take the secret out of retained UI state before the request starts; a rejection
    // reports the failure but never restores or keeps the password.
    const secret = password;
    const target = mode === 'new' ? {newAccount: Object.freeze({username, name, password: newPassword})} : {accountId: selected};
    held.busy = true;setBusy(true);setError(undefined);setPassword('');setNewPassword('');
    const caller = {isCurrent: () => held.live && witnessRef.current === held && !held.abort.signal.aborted, signal: held.abort.signal};
    void (async () => {
      try {
        await accept({...target, currentPassword: secret, expectedRevision: held.port.opening.ownerRevision}, caller);
        if (!held.live || witnessRef.current !== held) return;
        retire(held);
        held.onClose();
      } catch (failure) {
        if (!held.live || witnessRef.current !== held) return;
        held.busy = false;setBusy(false);setError(failure);
        held.onError?.(failure);
      }
    })();
  };
  const ready = Boolean(password && !busy && (mode === 'existing' ? selected : username.trim() && name.trim() && newPassword));
  return <Dialog open={open} onOpenChange={value => {if (!value) close();}} width={560} title={t('next.settingsSpec.server.signin.groups.0.rows.5.label')}
    dismissOnBack={!busy} dismissOnOutsidePress={!busy} showCloseButton={!busy}
    footer={<>
      <Button variant="glass" disabled={busy} onClick={close}>{t('action.cancel')}</Button>
      <Button variant="primary" disabled={!ready} loading={busy} onClick={submit}>{t('next.settingsSpec.server.signin.groups.0.rows.5.label')}</Button>
    </>}>
    <div className={styles.body}>
      <p className={styles.lede}>{t('next.settings.changeOwnerLocalBody')}</p>
      {error ? <p className={styles.error} role="alert">{errorMessage(error, 'generic', {operation: 'action'})}</p> : null}
      <LocalOwnerChoiceFields mode={mode} members={members} accountId={selected} username={username} name={name} newPassword={newPassword}
        disabled={busy} onModeChange={changeMode} onAccountIdChange={choose}
        onUsernameChange={value => {if (live() && !drawn.busy) setUsername(value);}} onNameChange={value => {if (live() && !drawn.busy) setName(value);}}
        onNewPasswordChange={value => {if (live() && !drawn.busy) setNewPassword(value);}} />
      <TextField label={t('account.currentPassword')}
        type="password" autoComplete="current-password" value={password} disabled={busy} onChange={event => typePassword(event.currentTarget.value)}/>
      <ul className={styles.lines}>
        <li><Icon name="i-check" size={18} />{t('accounts.settings.changeOwnerLocalStays')}</li>
        <li data-tone="info"><Icon name="i-info" size={18} />{t('accounts.settings.changeOwnerLocalOnlyNew')}</li>
      </ul>
    </div>
  </Dialog>;
}
