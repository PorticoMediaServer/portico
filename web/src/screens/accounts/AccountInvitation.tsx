import {useLayoutEffect, useState, useRef, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {JoinLink} from '@core/join-link.ts';
import type {HostedRequestApi} from '@core/portico-servers.ts';
import {AuthError, AuthStage, LocalInvitationForm} from '../../kit/accounts';
import {InvitationUnavailable, PorticoInvitation} from '../../kit/accounts/Invitation';
import {Button} from '../../kit/controls/Button';
import {createBrowserAccountEntry} from '../../platform/account-entry';

export type AccountInvitationScreenProps = Readonly<{
  /** The entry owner captures/scrubs href synchronously, then parses JoinLink. */
  link: JoinLink;
  account?: Readonly<{origin: string; accountId: string; username: string; request: HostedRequestApi['request']; isCurrent: () => boolean}>;
  signedOutContent: ReactNode;
  onBack: () => void; onDifferentAccount: () => void;
  /** No credential or directory URL crosses origins. The transient server
   * family has been retired before this actual proved destination is exposed. */
  onJoined: (destination: Readonly<{origin: string; signIn: 'local' | 'portico'}>) => void;
  /** Presentational only: a dialog keeps the same controller, preview, account
   * switch and journal, drawn as a bare stage inside the caller's dialog. */
  presentation?: 'page' | 'dialog';
  /** A closed preview refusal, returned only to the opening that supplied it. */
  onUnavailable?: () => void;
}>;

/** Standalone Services composition. Its caller remains account-only; this
 * component uses the same canonical guarded invitation/session controller. */
export function AccountInvitationScreen({link, account, signedOutContent, onBack, onDifferentAccount, onJoined, presentation, onUnavailable}: AccountInvitationScreenProps) {
  // A new invitation unmounts the old operation before any new interactive
  // render; no frame can submit the predecessor's private code.
  return <InvitationInstance key={JSON.stringify([link.server, link.id, link.code])} link={link} account={account} signedOutContent={signedOutContent} onBack={onBack} onDifferentAccount={onDifferentAccount} onJoined={onJoined} presentation={presentation} onUnavailable={onUnavailable} />;
}
function InvitationInstance(props: AccountInvitationScreenProps) {
  const [entry, setEntry] = useState<ReturnType<typeof createBrowserAccountEntry>>();
  useLayoutEffect(() => {
    const current = createBrowserAccountEntry({invitationDestination: 'server-origin'});
    setEntry(current);
    return () => current.dispose();
  }, []);
  return entry ? <InvitationOperation {...props} entry={entry} /> : <AuthStage legal={false} bare={props.presentation === 'dialog'}><p role="status">{defaultI18n.t('status.loading')}</p></AuthStage>;
}
function InvitationOperation({entry, link, account, signedOutContent, onBack, onDifferentAccount, onJoined, presentation = 'page', onUnavailable}: AccountInvitationScreenProps & {entry: ReturnType<typeof createBrowserAccountEntry>}) {
  const state = useSyncExternalStore(entry.subscribe, entry.getSnapshot), delivered = useRef<unknown>(undefined);
  const bare = presentation === 'dialog';
  const {server, id, code: invitationCode} = link;
  useLayoutEffect(() => {
    void entry.inspectInvitation({server, code: invitationCode, ...(id === undefined ? {} : {id})});
    return () => entry.cancel();
  }, [entry, server, id, invitationCode]);
  useLayoutEffect(() => {
    if (entry.getSnapshot() !== state || state.phase !== 'joined' || !state.joined || delivered.current === state.joined) return;
    delivered.current = state.joined; onJoined(state.joined);
  }, [entry, state, onJoined]);
  const code = state.error && typeof state.error === 'object' && 'code' in state.error ? state.error.code : undefined;
  useLayoutEffect(() => {
    if (entry.getSnapshot() === state && onUnavailable && (code === 'invitation_expired' || code === 'invitation_used')) onUnavailable();
  }, [entry, state, code, onUnavailable]);
  if (code === 'invitation_expired' || code === 'invitation_used' || code === 'invitation_for_another_account') return <InvitationUnavailable otherAccount={code === 'invitation_for_another_account'} onBack={onBack} onDifferentAccount={onDifferentAccount} bare={bare} />;
  const invitation = state.invitation;
  if (!invitation || !state.server) return <AuthStage legal={false} bare={bare}>{!state.error && <p role="status">{defaultI18n.t('status.loading')}</p>}<AuthError error={state.error} />{Boolean(state.error) && <Button onClick={() => void entry.inspectInvitation(link)}>{defaultI18n.t('action.tryAgain')}</Button>}</AuthStage>;
  if (invitation.signInMethod === 'local') return <LocalInvitationForm bare={bare} serverName={state.server.name} inviter={invitation.inviterUsername} libraries={invitation.libraries.map(library => library.name)} busy={state.busy}
    error={state.error} onJoin={(username, password, name) => void entry.joinLocal(username, password, name)} onBack={onBack} />;
  // MAIN's current preview exposes library names, not kinds. Do not guess an
  // icon from the name; exact phone-library presentation needs that producer.
  return <PorticoInvitation bare={bare} compact={bare} onCancel={onBack} serverName={state.server.name} libraries={invitation.libraries} member={invitation.role === 'member'} account={account?.isCurrent() ? account : undefined}
    signedOutContent={signedOutContent} busy={state.busy} error={state.error} onJoin={() => {if (account?.isCurrent()) void entry.joinPortico(account);}} onDifferentAccount={onDifferentAccount} />;
}
