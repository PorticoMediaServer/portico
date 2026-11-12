import {useLayoutEffect, useMemo, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {Account} from '@core/portico-account.ts';
import {RouteError, routeOrigin} from '@core/route-identity.ts';
import {inspectDirectServer} from '@core/server-connections.ts';
import {ServerSignInProbe} from '@core/sign-in.ts';
import {parseJoinLink} from '@core/join-link.ts';
import {parseShortInvitationCode} from '@core/short-invitation-code.ts';
import {LocalServerAddress} from '../../kit/accounts/ServerSwitcher';
import {ServicesStart} from '../../kit/accounts/ServicesWorkspace';
import {ServicesNotice} from '../../kit/accounts/ServicesAuth';
import {DocumentBootReady} from '../../shell/DocumentBootReady';
import {goTo} from '../../platform/hosted-navigation';
import {porticoAccountHolder} from '../../platform/portico-account-holder';
import {browserConnectionEnvironment} from '../../platform/server-connections';
import {distributionPage} from '../../services/distribution';
import {ShortInvitationEntry} from './ShortInvitationEntry';
import {carryInvitation} from '../../platform/invitation-carry';

const t = defaultI18n.t;
/** What the chooser at web.getportico.tv opens besides the list: `/servers#join`, `#setup` and
 * `#local`. Any other fragment is not one of these, and the list shows. */
export type ChooserIntent = 'join' | 'setup' | 'local';
export function chooserIntent(hash: string): ChooserIntent | undefined {
  return hash === '#join' ? 'join' : hash === '#setup' ? 'setup' : hash === '#local' ? 'local' : undefined;
}

/** A server's own page hands joining on to web.getportico.tv with `#invitation=<code>&address=<its
 * HTTPS address>` (`portico-handoff.ts`). The fragment is taken out of the address bar at once, and
 * the invitation is carried through the account's sign-in as a shared link's is. */
export function keepHandoffInvitation(): void {
  if (!['#invitation=', '#move=', '#ticket='].some(start => location.hash.startsWith(start))) return;
  const values = new URLSearchParams(location.hash.slice(1)), address = values.get('address') ?? '';
  history.replaceState(history.state, '', location.pathname + location.search);
  const invitation = values.get('invitation'), move = values.get('move'), ticket = values.get('ticket');
  if (invitation !== null && parseShortInvitationCode(invitation)) carryInvitation(invitation, address);
  // A waiting member's move code (the server's own page checked it with the server): moved here.
  else if (move !== null && /^[A-Za-z0-9]{12}$/.test(move.replace(/[-\s]/g, ''))) carryInvitation(move, address, Date.now(), 'move');
  // The server's ticket for an old password proved on its own page (P2-66); the password stayed there.
  else if (ticket !== null && /^[a-z2-7]{32,64}$/.test(ticket)) carryInvitation(ticket, address, Date.now(), 'ticket');
}

/** The drawn start (connect 26/31), for an account with no servers yet and for Set up: join with
 * an invitation, set up a server, or use a server by its address. */
export function ChooserStart({header, intent, onJoin, onLocal}: {header: React.ReactNode; intent?: 'setup'; onJoin: (code: string, address?: string) => void; onLocal: () => void}) {
  const [invitation, setInvitation] = useState(''), [invalid, setInvalid] = useState(false);
  useLayoutEffect(() => {if (intent === 'setup') document.getElementById('setup')?.scrollIntoView({block: 'start'});}, [intent]);
  // A pasted link and a typed code both open Join here: a link from its parts (the code and the
  // server it names), never by following it.
  const join = (value = invitation) => {
    const link = parseJoinLink(value.trim());
    if (link) {onJoin(link.code, link.server); return;}
    if (parseShortInvitationCode(value)) {onJoin(value); return;}
    setInvalid(true);
  };
  const share = async () => {if (navigator.share) await navigator.share({url: distributionPage}); else await navigator.clipboard.writeText(distributionPage);};
  return <DocumentBootReady><ServicesStart header={header} returning={intent === 'setup'} invitation={invitation}
    onInvitationChange={value => {setInvitation(value); setInvalid(false);}} onInvitationPaste={value => {setInvitation(value); join(value);}} onJoin={() => join()}
    onPaste={() => void navigator.clipboard.readText().then(value => {setInvitation(value); join(value);}, () => {})}
    onDownload={() => location.assign(distributionPage)} onApps={() => location.assign(distributionPage)}
    onCopyDownload={() => void navigator.clipboard.writeText(distributionPage).catch(() => {})} onShareDownload={() => void share().catch(() => {})}
    error={invalid ? <ServicesNotice tone="error">{t('web.link.invalid')}</ServicesNotice> : undefined}
    notice={<button type="button" data-quiet onClick={onLocal}>{t('web.chooser.localServer')}</button>} /></DocumentBootReady>;
}

/** Join with an invitation code at web.getportico.tv, as the Portico Account signed in here. */
export function ChooserJoin({who, initialCode, initialAddress, onBack}: {who: Account; initialCode?: string; initialAddress?: string; onBack: () => void}) {
  const account = useMemo(() => Object.freeze({...porticoAccountHolder.access(who.id), username: who.username}), [who]);
  return <ShortInvitationEntry account={account} initialCode={initialCode} initialAddress={initialAddress} onBack={onBack}
    onDifferentAccount={() => void porticoAccountHolder.signOut().then(() => goTo('/sign-in?next=' + encodeURIComponent('/servers#join'), {replace: true}), () => {})}
    // A Portico Accounts server joined here is opened from the list here; a local one at its own address.
    onJoined={joined => {if (joined.signIn === 'portico') goTo('/servers', {replace: true}); else location.assign(joined.origin);}} />;
}

/** A server that uses local sign-in keeps its own app at its own address: the typed address is
 * checked to be that Portico server, then the person goes there. */
export function ChooserLocalAddress({onBack}: {onBack: () => void}) {
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  return <LocalServerAddress busy={busy} error={error} onBack={onBack} onContinue={address => {
    if (busy) return;
    setBusy(true); setError(undefined);
    void localServerOrigin(address).then(origin => location.assign(origin + '/'), failure => {setError(failure); setBusy(false);});
  }} />;
}
async function localServerOrigin(address: string): Promise<string> {
  const probe = new ServerSignInProbe(), signal = AbortSignal.timeout(15000);
  try {
    const system = await probe.inspect(address), state = probe.getSnapshot();
    if (!system || !state.origin) throw state.error ?? new RouteError('route_unavailable', 'The server did not answer.');
    const origin = routeOrigin(state.origin), pin = await inspectDirectServer(origin, browserConnectionEnvironment(system.id), signal);
    if (pin.serverId !== system.id) throw new RouteError('identity_mismatch', 'The server identity changed.');
    return origin;
  } finally {probe.dispose();}
}
