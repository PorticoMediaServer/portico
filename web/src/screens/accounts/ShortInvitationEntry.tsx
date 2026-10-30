import {useEffect, useRef, useState, useSyncExternalStore, type ChangeEvent, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {HostedRequestApi} from '@core/portico-servers.ts';
import {formatShortInvitationCode} from '@core/short-invitation-code.ts';
import {AuthError, AuthLinks, AuthStage} from '../../kit/accounts/AuthStage';
import {LocalInvitationForm, PorticoInvitation} from '../../kit/accounts/Invitation';
import {Button, TextField} from '../../kit/controls';
import {createBrowserAccountEntry, type BrowserAccountEntry} from '../../platform/account-entry';
import styles from '../../kit/accounts/short-invitation-entry.module.css';
import entryStyles from './short-invitation-entry.module.css';

const t = defaultI18n.t;
const CODE_PLACEHOLDER = t('web.directJoin.codePlaceholder');
export const shortInvitationCodePlaceholder = CODE_PLACEHOLDER;
/** The field is submitted verbatim. The checked codec decides case, separators,
 * ambiguous symbols and long-link refusal; this UI never truncates or rewrites. */
export const shortInvitationCodeForSubmit = (value: string): string => value;
/** Presentation only, and only after the complete value passed the codec. */
export const shortInvitationCodeAfterBlur = (value: string): string => formatShortInvitationCode(value) ?? value;
const REFUSAL_CODES: ReadonlySet<string> = new Set(['invitation_not_found', 'invalid_request', 'invitation_expired', 'invitation_used', 'invitation_for_another_account']);
export function shortInvitationRefusal(error: unknown): boolean {
  const code = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
  return typeof code === 'string' && REFUSAL_CODES.has(code);
}
/** The two approved generic sentences; everything else keeps the typed error presentation. */
export function shortInvitationFormErrorKind(phase: string, error: unknown, invitation: unknown, busy: boolean): 'invalid' | 'unavailable' | 'typed' | undefined {
  if (!error || busy) return undefined;
  const code = error && typeof error === 'object' && 'code' in error ? error.code : undefined;
  if (phase === 'address') return code === 'invitation_incomplete' ? 'invalid' : 'typed';
  if ((phase === 'password' || phase === 'portico') && !invitation) return shortInvitationRefusal(error) ? 'unavailable' : 'typed';
  return undefined;
}

export type ShortInvitationEntryProps = Readonly<{
  onBack: () => void;
  onJoined: (destination: Readonly<{origin: string; signIn: 'local' | 'portico'}>) => void;
  account?: Readonly<{origin: string; accountId: string; username: string; request: HostedRequestApi['request']; isCurrent: () => boolean}>;
  signedOutContent?: ReactNode;
  onDifferentAccount?: () => void;
  /** Test seam only; the real entry keeps its own browser custody. */
  createEntry?: () => BrowserAccountEntry;
  initialAddress?: string;
  initialCode?: string;
}>;

/** The ruling-10 initial Join step. The same entry instance carries the
 * verified origin into the existing local/Portico invitation acceptance. */
export function ShortInvitationEntry(props: ShortInvitationEntryProps) {
  const {createEntry} = props;
  const [entry, setEntry] = useState<BrowserAccountEntry>();
  useEffect(() => {
    const current = (createEntry ?? (() => createBrowserAccountEntry({invitationDestination: 'server-origin'})))();
    setEntry(current);
    return () => current.dispose();
  }, [createEntry]);
  return entry ? <ShortInvitationOperation {...props} entry={entry} /> : null;
}

function ShortInvitationOperation({entry, onBack, onJoined, account, signedOutContent, onDifferentAccount, initialAddress = '', initialCode = ''}: ShortInvitationEntryProps & {entry: BrowserAccountEntry}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot);
  const [address, setAddress] = useState(initialAddress), [code, setCode] = useState(initialCode);
  const delivered = useRef<unknown>(undefined);
  useEffect(() => {
    if (state.phase !== 'joined' || !state.joined || delivered.current === state.joined) return;
    delivered.current = state.joined;
    onJoined(state.joined);
  }, [state.phase, state.joined, onJoined]);
  const busy = state.busy || state.phase === 'checking';
  const back = () => {if (busy) return; entry.cancel(); onBack();};
  const refusal = shortInvitationRefusal(state.error);
  const refusalNotice = refusal ? <p className={styles.error} role="alert">{t('web.directJoin.entryUnavailable')}</p> : undefined;
  const errorKind = shortInvitationFormErrorKind(state.phase, state.error, state.invitation, busy);
  const formError = errorKind === 'invalid' ? <p className={styles.error} role="alert">{t('web.directJoin.entryInvalid')}</p>
    : errorKind === 'unavailable' ? refusalNotice
    : errorKind === 'typed' ? <AuthError error={state.error} /> : undefined;
  if (state.phase === 'joined') return null;
  if (state.invitation && state.server) {
    const returnBack = () => {if (state.busy) return;entry.cancel();};
    if (state.invitation.signInMethod === 'local') return <LocalInvitationForm serverName={state.server.name} inviter={state.invitation.inviterUsername}
      libraries={state.invitation.libraries.map(library => library.name)} busy={state.busy} error={refusal ? undefined : state.error} errorContent={refusalNotice}
      onJoin={(username, password, name) => void entry.joinLocal(username, password, name)} onBack={returnBack} />;
    return <PorticoInvitation serverName={state.server.name} libraries={state.invitation.libraries} member={state.invitation.role === 'member'}
      account={account?.isCurrent() ? account : undefined} signedOutContent={signedOutContent} busy={state.busy} error={refusal ? undefined : state.error} errorContent={refusalNotice}
      onJoin={() => {if (account?.isCurrent()) void entry.joinPortico(account);}} onDifferentAccount={() => {if (state.busy) return;entry.cancel();onDifferentAccount?.();}} />;
  }
  const change = (apply: (value: string) => void) => (event: ChangeEvent<HTMLInputElement>) => {
    if (busy || state.error) entry.cancel();
    apply(event.currentTarget.value);
  };
  return <AuthStage phoneShape="top" className={[styles.stage, entryStyles.stage].filter(Boolean).join(' ')} contentClassName={styles.content}>
    <h1>{t('web.directJoin.title')}</h1>
    <p className={styles.lede}>{t('web.directJoin.entryBody')}</p>
    {formError}
    <form className={styles.form} onSubmit={event => {
      event.preventDefault();
      if (busy || !address.trim() || !code) return;
      void entry.inspectShortInvitation(address.trim(), shortInvitationCodeForSubmit(code));
    }}>
      <TextField variant="auth" label={t('web.direct.serverAddress')} name="server" autoComplete="url" inputMode="url" autoCapitalize="none" spellCheck={false}
        value={address} onChange={change(setAddress)} disabled={busy} required />
      <TextField variant="auth" label={t('web.directJoin.code')} name="code" autoComplete="off" inputMode="text" autoCapitalize="characters" spellCheck={false}
        placeholder={CODE_PLACEHOLDER} value={code} onChange={change(setCode)} onBlur={() => {if (!busy) setCode(shortInvitationCodeAfterBlur(code));}} disabled={busy} required />
      <Button className={styles.submit} variant="primary" type="submit" loading={busy} disabled={!address.trim() || !code}>{t('action.continue')}</Button>
      {busy && <p className={styles.checking} role="status">{t('web.directJoin.checking')}</p>}
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={back}>{t('action.back')}</button></AuthLinks>
  </AuthStage>;
}
