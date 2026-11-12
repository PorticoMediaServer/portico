import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {Account} from '@core/portico-account.ts';
import type {PorticoMoveProof} from '@core/portico-server-sign-in.ts';
import {AuthError, AuthLinks, AuthStage} from '../../kit/accounts/AuthStage';
import {Button, TextField} from '../../kit/controls';
import {createBrowserAccountEntry} from '../../platform/account-entry';
import {porticoAccountHolder} from '../../platform/portico-account-holder';
import {AccountSignInView} from './AccountSignInView';
import {AccountSignInForm} from './sign-in-form';

const t = defaultI18n.t;
/** At web.getportico.tv: a member waiting on a server that switched to Portico Accounts moves their
 * account there to this Portico Account, with the move code the owner gave them. The server is
 * looked up at its address and proves its key before its name is shown; the proof goes only to
 * that server. A code is checked (`move/check`), a ticket (which the server answered for the member's
 * old password on its own page, P2-66) is named by the server without being spent
 * (`move/ticket`). Either way the page then always stops on a confirm built only from what it can
 * trust: the account's name from the server, the server's name from its own system answer after
 * its key check, and this page's own Portico Account. Arriving by a link never skips it, and
 * nothing goes to `move/complete` until the person presses Move. */
export function ChooserMove({who, held, initialAddress, onBack, onNotYou}: {who: Account; held: PorticoMoveProof; initialAddress?: string; onBack: () => void; onNotYou: () => void}) {
  const [opened] = useState(() => {const entry = createBrowserAccountEntry(); return {entry, form: new AccountSignInForm(entry)};});
  useEffect(() => () => {opened.form.dispose(); opened.entry.dispose();}, [opened]);
  const {entry, form} = opened, state = useSyncExternalStore(entry.subscribe, entry.getSnapshot);
  const access = useMemo(() => porticoAccountHolder.access(who.id), [who]);
  const ticket = 'ticket' in held ? held.ticket : undefined;
  const [address, setAddress] = useState(initialAddress ?? ''), [code, setCode] = useState('code' in held ? held.code : '');
  const proof: PorticoMoveProof = ticket !== undefined ? {ticket} : {code: code.trim()};
  const ready = !!address.trim() && (ticket !== undefined || !!code.trim());
  const busy = state.busy || state.phase === 'checking';
  // Moved: the sign-in it answered with opens as any other (Who's watching, a PIN, Home).
  if (['profiles', 'ready', 'challenge', 'two-step-setup'].includes(state.phase)) return <AccountSignInView entry={entry} form={form} initialize={false} onCancel={onBack} />;
  if (state.phase === 'move-portico' && state.server && state.movePreview) return <AuthStage phoneShape="top">
    <h1>{t('accounts.move.porticoQuestion', {username: state.movePreview.username, server: state.server.name, account: who.username})}</h1>
    <p>{t('accounts.move.porticoConfirm', {username: state.movePreview.username, server: state.server.name, account: who.username})}</p>
    <AuthError error={state.error} />
    <Button variant="primary" loading={busy} onClick={() => void entry.movePortico(proof, access)}>{t('accounts.move.porticoAction')}</Button>
    <AuthLinks>
      <button type="button" disabled={busy} onClick={onNotYou}>{t('accounts.invitation.notYou')}</button>
      <button type="button" disabled={busy} onClick={() => {entry.cancel(); onBack();}}>{t('action.cancel')}</button>
    </AuthLinks>
  </AuthStage>;
  return <AuthStage phoneShape="top"><h1>{t(ticket !== undefined ? 'accounts.move.finishTitle' : 'accounts.move.enterCode')}</h1>
    <p>{t(ticket !== undefined ? 'accounts.move.ticketBody' : 'accounts.move.porticoCodeBody')}</p>
    <AuthError error={state.error} />
    <form onSubmit={event => {
      event.preventDefault();
      if (busy || !ready) return;
      void (async () => {await entry.inspect(address.trim()); if ('code' in proof) await entry.previewMoveCode(proof.code); else await entry.previewMoveTicket(proof.ticket);})();
    }}>
      <TextField variant="auth" label={t('accounts.signIn.address')} name="server" autoComplete="url" inputMode="url" autoCapitalize="none" spellCheck={false}
        value={address} onChange={event => setAddress(event.currentTarget.value)} disabled={busy} required />
      {ticket === undefined && <TextField variant="auth" label={t('accounts.move.code')} name="move-code" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false}
        value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} required />}
      <Button variant="primary" type="submit" loading={busy} disabled={!ready}>{t('action.continue')}</Button>
    </form>
    <AuthLinks><button type="button" disabled={busy} onClick={onBack}>{t('action.back')}</button></AuthLinks>
  </AuthStage>;
}
