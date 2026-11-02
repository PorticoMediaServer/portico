import {useState} from 'react';
import {defaultI18n} from '@i18n';
import type {SignInKind} from '@core/app-sessions.ts';
import {AuthError, AuthLinks, AuthStage} from './AuthStage';
import {Dialog} from '../overlays';
import {Button, TextField} from '../controls';
import {Icon} from '../Icon';
import styles from './accounts.module.css';

const t = defaultI18n.t;
export type ServerSwitcherChoice = Readonly<{id: string; label: string; signIn: SignInKind; selected: boolean}>;
/** Auth22's actual saved sign-ins. No cached route is promoted to online or to
 * a Hosted directory record; unavailable last-seen/profile facts stay absent. */
export function ServerSwitcher({choices, busy, error, onChoose, onRefresh, onLocal, onClose}: {
  choices: readonly ServerSwitcherChoice[]; busy?: boolean; error?: unknown;
  onChoose: (choice: ServerSwitcherChoice) => void; onRefresh: () => void; onLocal: () => void; onClose: () => void;
}) {
  return <AuthStage legal={false}><Dialog open title={t('servers.switch')} width={600} onOpenChange={open => {if (!open) onClose();}}
    footer={<><Button variant="text" size="small" icon="i-refresh" disabled={busy} onClick={onRefresh}>{t('accounts.servers.refresh')}</Button>
      <Button size="small" disabled={busy} onClick={onLocal}>{t('accounts.signIn.local')}</Button></>}>
    <AuthError error={error} />
    {(['portico', 'local'] as const).map(kind => {
      const rows = choices.filter(choice => choice.signIn === kind);
      return rows.length > 0 && <section key={kind} className={styles.serverGroup}><h3>{t(kind === 'portico' ? 'web.servers.accountServers' : 'accounts.servers.local')}</h3>
        {rows.map(choice => <button key={choice.id} type="button" className={styles.serverRow} data-current={choice.selected || undefined}
          disabled={busy || choice.selected} aria-current={choice.selected || undefined} onClick={() => onChoose(choice)}>
          <span className={styles.serverIcon}><Icon name="i-server" size={20} /></span><span>{choice.label}<small>{choice.selected ? t('web.servers.current') : t(kind === 'local' ? 'accounts.session.local' : 'accounts.session.portico')}</small></span>
          {!choice.selected && <Icon name="i-right" size={20} />}
        </button>)}
      </section>;
    })}
  </Dialog></AuthStage>;
}
/** Connect39b: looking up an address must finish before any credential form. */
export function LocalServerAddress({busy, error, onContinue, onBack}: {busy?: boolean; error?: unknown; onContinue: (address: string) => void; onBack: () => void}) {
  const [address, setAddress] = useState('');
  return <AuthStage legal={false}><h1>{t('accounts.signIn.local')}</h1><AuthError error={error} />
    <form className={styles.form} onSubmit={event => {event.preventDefault(); if (address.trim() && !busy) onContinue(address);}}>
      <TextField variant="auth" label={t('accounts.signIn.address')} name="server-address" inputMode="url" autoComplete="url" value={address} onChange={event => setAddress(event.currentTarget.value)} disabled={busy} autoFocus required />
      <Button className={styles.submit} variant="primary" type="submit" loading={busy} disabled={!address.trim()}>{t('action.continue')}</Button>
    </form><AuthLinks><button type="button" disabled={busy} onClick={onBack}>{t('accounts.signIn.back')}</button></AuthLinks>
  </AuthStage>;
}
