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

const t = defaultI18n.t;
/** What the chooser at web.getportico.tv opens besides the list: `/servers#join`, `#setup` and
 * `#local`. Any other fragment is not one of these, and the list shows. */
export type ChooserIntent = 'join' | 'setup' | 'local';
export function chooserIntent(hash: string): ChooserIntent | undefined {
  return hash === '#join' ? 'join' : hash === '#setup' ? 'setup' : hash === '#local' ? 'local' : undefined;
}

/** The drawn start (connect 26/31), for an account with no servers yet and for Set up: join with
 * an invitation, set up a server, or use a server by its address. */
export function ChooserStart({header, intent, onJoin, onLocal}: {header: React.ReactNode; intent?: 'setup'; onJoin: (code?: string) => void; onLocal: () => void}) {
  const [invitation, setInvitation] = useState(''), [invalid, setInvalid] = useState(false);
  useLayoutEffect(() => {if (intent === 'setup') document.getElementById('setup')?.scrollIntoView({block: 'start'});}, [intent]);
  // A link opens the server's own page, where the invitation is read; a code is entered here.
  const join = (value = invitation) => {
    const link = parseJoinLink(value.trim());
    if (link) {location.assign(value.trim()); return;}
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
export function ChooserJoin({who, initialCode, onBack}: {who: Account; initialCode?: string; onBack: () => void}) {
  const account = useMemo(() => Object.freeze({...porticoAccountHolder.access(who.id), username: who.username}), [who]);
  return <ShortInvitationEntry account={account} initialCode={initialCode} onBack={onBack}
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
