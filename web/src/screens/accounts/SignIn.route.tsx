import {useLayoutEffect, useState} from 'react';
import {defineWebScreen} from '../../route-registry';
import {useWebScreenContext} from '../../route-registry-view';
import {createBrowserAccountEntry, type BrowserAccountEntry} from '../../platform/account-entry';
import {takeLocalMoveHandoff} from '../../platform/local-move-continuation';
import {discardTwoStepSetup, requestTwoStepSetup} from '../../platform/two-step-continuation';
import {router} from '../../router';
import {AccountSignInForm} from './sign-in-form';
import {AccountSignInView} from './AccountSignInView';
import {currentWebEnvironment} from '../../platform/environment';

type Instance = Readonly<{entry: BrowserAccountEntry; form: AccountSignInForm; continuation: boolean}>;

function SignInScreen() {
  const {takeAccountReturn, takeAccountMove} = useWebScreenContext();
  const [instance, setInstance] = useState<Instance>();
  // Layout lifetime: the ticket is this exact mount's. A replayed effect creates a
  // new ticket, so a disposed entry's late completion can never navigate its successor.
  useLayoutEffect(() => {
    const ticket = {retired: false};
    const handoff = takeLocalMoveHandoff();
    if (handoff) {
      const owned: Instance = {entry: handoff.entry, form: handoff.form, continuation: true};
      setInstance(owned);
      // Recorded before admission, so whichever screen the sign-in lands on sees it.
      if (handoff.twoStep) requestTwoStepSetup(handoff.custody.target); else discardTwoStepSetup();
      void handoff.entry.completeLocalOwnerMove(handoff.custody).then(admitted => {
        // A stale grant was refused and retired; show the server the app really has.
        if (!admitted) discardTwoStepSetup();
        if (!ticket.retired && !admitted) void router.navigate({to: '/servers', replace: true});
      }).catch(() => {discardTwoStepSetup(); if (!ticket.retired) void router.navigate({to: '/servers', replace: true});});
      return () => {ticket.retired = true;handoff.close();};
    }
    const entry = createBrowserAccountEntry(), form = new AccountSignInForm(entry);
    setInstance({entry, form, continuation: false});
    return () => {ticket.retired = true;form.dispose();entry.dispose();};
  }, []);
  return instance ? <AccountSignInView entry={instance.entry} form={instance.form} initialize={!instance.continuation} takeReturn={takeAccountReturn} takeMove={takeAccountMove} /> : null;
}
/** At web.getportico.tv /sign-in is the Portico Account sign-in, drawn by the account pages in
 * this same document (H14); a server is then opened from the chooser. This screen draws and does
 * nothing there, even for the moment the router keeps it while leaving. */
function HostedSignIn() {return null;}
function SignInRoute() {return currentWebEnvironment().hostedWeb ? <HostedSignIn /> : <SignInScreen />;}
export default defineWebScreen({id: 'signIn', component: SignInRoute, chrome: 'standalone'});
