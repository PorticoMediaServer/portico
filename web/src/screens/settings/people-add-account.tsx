import {useId, useRef, useState, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {directMemberCreateFailure, directMemberInputReady, type CreatedDirectMember, type NewDirectMember} from '@core/direct-member-create.ts';
import {Button, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {usePhone} from '../../kit/overlays/shared';
import {AuthPasswordField} from '../../kit/accounts/AuthStage';
import {ServicesStrength} from '../../kit/accounts/ServicesAuth';
import styles from './people-invite.module.css';

const t = defaultI18n.t;

export type AddAccount = (input: NewDirectMember, signal: AbortSignal) => Promise<CreatedDirectMember>;

/** owner-people frames 6 and 25 (server-people "Add account", local-sign-in servers only): Username, Profile
 * name, Password with the strength meter every password in Portico uses, and Libraries they can see (All
 * libraries, including ones added later; unticking lists them). Add account stays disabled until the username
 * is valid, the profile has a name and the password is strong enough. A taken username comes back as a plain
 * error under the first field. It writes nothing until Add account. */
export function AddAccountDialog({open, onOpenChange, libraries, add, onAdded, returnFocusRef}: Readonly<{
  open: boolean; onOpenChange: (open: boolean) => void; libraries: readonly {id: string; name: string}[];
  add: AddAccount; onAdded: (account: CreatedDirectMember) => void; returnFocusRef?: RefObject<HTMLElement | null>;
}>) {
  const [username, setUsername] = useState(''), [name, setName] = useState(''), [password, setPassword] = useState('');
  const [all, setAll] = useState(true), [chosen, setChosen] = useState<readonly string[]>([]);
  const [busy, setBusy] = useState(false), [failure, setFailure] = useState<'taken' | 'failed'>();
  const request = useRef<AbortController | undefined>(undefined), strength = useId();
  const input: NewDirectMember = {username, name, password, allowedLibraries: all ? [] : chosen};
  const ready = directMemberInputReady(input) && passwordStrength(password).acceptable && (all || chosen.length > 0);
  const reset = () => {setUsername(''); setName(''); setPassword(''); setAll(true); setChosen([]); setFailure(undefined);};
  const close = () => {if (busy) return; request.current?.abort(); reset(); onOpenChange(false);};
  const submit = () => {
    if (!ready || busy) return;
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setFailure(undefined);
    void add(input, controller.signal).then(account => {
      if (controller.signal.aborted) return;
      reset(); onOpenChange(false); onAdded(account);
    }, error => {
      if (controller.signal.aborted) return;
      setFailure(directMemberCreateFailure(error));
    }).finally(() => {if (request.current === controller) {request.current = undefined; setBusy(false);}});
  };
  const phone = usePhone();
  const cancel = <Button size="small" variant={phone ? 'text' : 'glass'} className={phone ? styles.sheetCancel : undefined} disabled={busy} onClick={close}>{t('next.settings.cancel')}</Button>;
  const confirm = <Button size="small" variant="primary" className={phone ? styles.sheetConfirm : undefined} disabled={!ready} loading={busy} onClick={submit}>{t('web.people.addAccount')}</Button>;
  // Phone (frame 25): Cancel at the sheet's top right in place of Done, and Add account alone at its foot.
  return <Dialog open={open} onOpenChange={value => {if (!value) close();}} title={t('web.people.addAccountTitle')} description={t('next.settings.people.addAccountLede')}
    returnFocusRef={returnFocusRef} dismissOnBack={!busy} {...(phone ? {headerAction: cancel, showCloseButton: false, stackFooter: true} : {})}
    footer={phone ? confirm : <>{cancel}{confirm}</>}>
    <form className={`${styles.form} ${styles.addForm}`} onSubmit={event => {event.preventDefault(); submit();}}>
      {failure === 'failed' && <p role="alert">{t('next.settings.people.addAccountFailed')}</p>}
      <TextField label={t('accounts.signIn.username')} description={t('next.settings.people.usernameHelp')} autoComplete="off" autoCapitalize="none" spellCheck={false}
        value={username} disabled={busy} error={failure === 'taken' ? t('web.directJoin.error.taken') : undefined}
        onChange={event => {setUsername(event.currentTarget.value); if (failure === 'taken') setFailure(undefined);}} />
      <TextField label={t('web.people.profileName')} description={t('web.people.profileNameHelp')} value={name} disabled={busy} onChange={event => setName(event.currentTarget.value)} />
      <div>
        <AuthPasswordField label={t('accounts.signIn.password')} autoComplete="new-password" value={password} disabled={busy} aria-describedby={strength}
          onChange={event => setPassword(event.currentTarget.value)} />
        <ServicesStrength password={password} id={strength} />
      </div>
      <fieldset disabled={busy} className={styles.fieldset}>
        <legend>{t('web.people.visibleLibraries')}</legend>
        <label className={styles.choice}>
          <input type="checkbox" checked={all} onChange={event => {setAll(event.currentTarget.checked); if (event.currentTarget.checked) setChosen([]);}} />
          <span>{t('web.people.allLibrariesLater')}</span>
        </label>
        {!all && libraries.map(library => <label key={library.id} className={styles.choice}>
          <input type="checkbox" checked={chosen.includes(library.id)} onChange={event => {
            const checked = event.currentTarget.checked;
            setChosen(current => checked ? [...current, library.id] : current.filter(id => id !== library.id));
          }} />
          <span>{library.name}</span>
        </label>)}
      </fieldset>
    </form>
  </Dialog>;
}
