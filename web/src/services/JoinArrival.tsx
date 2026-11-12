import {useLayoutEffect} from 'react';
import {parseJoinLink} from '@core/join-link.ts';
import {goTo} from '../platform/hosted-navigation';
import {carryInvitation} from '../platform/invitation-carry';
import {ServicesAuthStage} from '../kit/accounts/ServicesAuth';

/** A shared invitation link, `https://web.getportico.tv/join#code=…&server=…` (settings/invitations.ts).
 * Its fragment is taken out of the address at once; the code and the server it names go on to Join,
 * through the account's sign-in when there is none. */
export function JoinArrival() {
  useLayoutEffect(() => {
    const link = parseJoinLink('https://web.getportico.tv/join' + location.hash);
    history.replaceState(history.state, '', location.pathname);
    if (link) carryInvitation(link.code, link.server);
    goTo(link ? '/servers' : '/servers#join', {replace: true});
  }, []);
  return <ServicesAuthStage><></></ServicesAuthStage>;
}
