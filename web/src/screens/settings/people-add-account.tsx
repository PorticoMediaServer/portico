import {useId, useRef, useState, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {directMemberCreateFailure, directMemberInputReady, type CreatedDirectMember, type NewDirectMember} from '@core/direct-member-create.ts';
import {Button, TextField} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
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
  const [busy, setBusy] = useState(false), [failure, setFailure] = useState<'taken' | 'failed'>(), [picking, setPicking] = useState(false);
  const request = useRef<AbortController | undefined>(undefined), strength = useId();
  const input: NewDirectMember = {username, name, password, allowedLibraries: all ? [] : chosen};
  const ready = directMemberInputReady(input) && passwordStrength(password).acceptable && (all || chosen.length > 0);
  const reset = () => {setUsername(''); setName(''); setPassword(''); setAll(true); setChosen([]); setFailure(undefined); setPicking(false);};
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
    {phone && picking ? <div className={`${styles.form} ${styles.addForm}`}>
      <div className={styles.pickHead}><Button size="small" variant="text" onClick={() => setPicking(false)}>{t('next.overlays.done')}</Button></div>
      <LibraryChecks libraries={libraries} all={all} chosen={chosen} disabled={busy} onAll={value => {setAll(value); if (value) setChosen([]);}} onChosen={setChosen} />
    </div> : <form className={`${styles.form} ${styles.addForm}`} onSubmit={event => {event.preventDefault(); submit();}}>
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
      <AddAccountLibraries phone={phone} libraries={libraries} all={all} chosen={chosen} disabled={busy}
        onAll={value => {setAll(value); if (value) setChosen([]);}} onChosen={setChosen} onPick={() => setPicking(true)} />
    </form>}
  </Dialog>;
}

type LibraryProps = Readonly<{libraries: readonly {id: string; name: string}[]; all: boolean; chosen: readonly string[]; disabled: boolean;
  onAll: (all: boolean) => void; onChosen: (ids: readonly string[]) => void}>;

/** Frame 6 (desktop): "All libraries, including ones added later", and unticking it lists the libraries. */
function LibraryChecks({libraries, all, chosen, disabled, onAll, onChosen}: LibraryProps) {
  return <fieldset disabled={disabled} className={styles.fieldset}>
    <legend>{t('web.people.visibleLibraries')}</legend>
    <label className={styles.choice}>
      <input type="checkbox" checked={all} onChange={event => onAll(event.currentTarget.checked)} />
      <span>{t('web.people.allLibrariesLater')}</span>
    </label>
    {!all && libraries.map(library => <label key={library.id} className={styles.choice}>
      <input type="checkbox" checked={chosen.includes(library.id)} onChange={event => {
        const checked = event.currentTarget.checked;
        onChosen(checked ? [...chosen, library.id] : chosen.filter(id => id !== library.id));
      }} />
      <span>{library.name}</span>
    </label>)}
  </fieldset>;
}

/** The libraries control: frame 6's checkboxes on a desktop; on a phone frame 25's grouped row, "Libraries they can
 * see" with its value and a chevron, which opens the same choice in the sheet. */
export function AddAccountLibraries({phone, onPick, ...props}: LibraryProps & Readonly<{phone: boolean; onPick: () => void}>) {
  if (!phone) return <LibraryChecks {...props} />;
  const value = props.all ? t('next.settings.people.allLibraries') : t('next.settings.invitations.libraries', {count: props.chosen.length});
  return <button type="button" className={styles.pickRow} disabled={props.disabled} onClick={onPick}>
    <span>{t('web.people.visibleLibraries')}</span><span className={styles.pickValue}>{value}</span><Icon name="i-right" size={18} />
  </button>;
}
