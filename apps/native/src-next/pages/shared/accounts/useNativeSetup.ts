import {useEffect, useState, useSyncExternalStore} from 'react';
import {AppState, Linking} from 'react-native';
import {createSetupController, type SetupController} from '../../../../../../packages/client-core/src/setup-controller';
import {setupStartOverOffer} from '../../../../../../packages/client-core/src/presentation/setup-messages';
import {passwordStrength} from '../../../../../../packages/client-core/src/password-strength';
import {useTheme} from '../../../kit/theme';
import {nativeAccounts} from '../../../platform/accounts';
import {createNativeSetupAdapter, type NativeSetupTarget} from '../../../platform/setup-adapter';

/** First-run setup's controller for one target: created with the app's foreground state, told when it changes, started
 * once, and disposed (with its adapter) when the target changes or the page leaves. Moved from the old screen. */
export function useSetupController(target: NativeSetupTarget) {
  const [controller, setController] = useState<SetupController>();
  useEffect(() => {
    let live = true;
    const adapter = createNativeSetupAdapter(target, {current: () => live});
    const created = createSetupController(adapter.ports, {foreground: AppState.currentState === 'active'});
    const app = AppState.addEventListener('change', state => created.setForeground(state === 'active'));
    setController(created);
    created.start();
    return () => {live = false; app.remove(); created.dispose(); adapter.dispose();};
  }, [target]);
  return controller;
}

const noSubscription = () => () => {};

/** Everything a setup state needs: the server decides every state; this keeps what the owner types and chooses, and the
 * actions each state offers. Moved from the old screen as it was. */
export function useSetupStates({controller, target, onAdmitted, onLeave}: {controller: SetupController; target: NativeSetupTarget; onAdmitted: (serverId: string) => void; onLeave: () => void}) {
  const phone = useTheme().platform === 'phone';
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const busy = useSyncExternalStore(controller.subscribe, controller.busy);
  const credentials = nativeAccounts.accountCredentials();
  const signedIn = useSyncExternalStore(credentials?.subscribe ?? noSubscription, () => !!credentials?.getSnapshot().session);
  const [name, setName] = useState(target.server.name);
  const [username, setUsername] = useState(''), [password, setPassword] = useState(''), [confirm, setConfirm] = useState('');
  const [notNow, setNotNow] = useState(false), [startingOver, setStartingOver] = useState(false), [setupCode, setSetupCode] = useState('');
  const [chosen, setChosen] = useState<'hosted' | 'local'>(target.server.hostedConfigured ? 'hosted' : 'local');
  // O-169, step 1 continued: unset follows the sign-in kind (On for Portico Accounts, Off for local sign-in).
  const [accessChoice, setAccessChoice] = useState<'on' | 'private' | 'off'>(), [securityChoice, setSecurityChoice] = useState<'enhanced' | 'standard'>('enhanced');

  // Admitted: setup's own part is over, and the first library follows in the shell.
  useEffect(() => {if (view.kind === 'library' || view.kind === 'done') onAdmitted(target.server.id);}, [view.kind, onAdmitted, target.server.id]);
  useEffect(() => {if (view.kind !== 'awaiting_approval') setNotNow(false);}, [view.kind]);

  // Back in the form steps through it as on the web: the owner form back to the choice, the choice out of setup.
  const back = () => {
    if (view.kind !== 'form') controller.back();
    else if (view.mode === 'local') controller.chooseKind('choose');
    else onLeave();
  };
  // Start over (auth.html 1e panels 3–4): it asks first only where the setup was approved.
  const offer = setupStartOverOffer(view);
  const remoteAccess = accessChoice ?? ((view.kind === 'form' || view.kind === 'starting') && view.mode === 'local' || chosen === 'local' ? 'off' : 'on');
  const remote = {remoteAccess, remoteSecurity: securityChoice} as const;
  const session = credentials?.getSnapshot().session;
  return {phone, view, busy, signedIn, offer, name,
    setName, username, setUsername, password, setPassword, confirm, setConfirm, setupCode, setSetupCode,
    chosen, setChosen, remoteAccess, setAccessChoice, securityChoice, setSecurityChoice,
    notNow, notNowPressed: () => setNotNow(true), startingOver, setStartingOver,
    accountName: session?.account.displayName || session?.account.username || '',
    localValid: !!name.trim() && /^[^\s@]{3,64}$/.test(username.trim()) && passwordStrength(password).acceptable && password === confirm,
    back, retry: controller.retry, startAgain: controller.startAgain,
    startOver: () => {if (offer.confirm) setStartingOver(true); else controller.startOver();},
    confirmStartOver: () => {setStartingOver(false); controller.startOver();},
    submitCode: () => controller.submitCode(setupCode), lockoutFix: controller.lockoutFix, approveInApp: controller.approveInApp,
    openApproval: (link: string) => {void Linking.openURL(link).catch(() => {});},
    createOwner: () => {const secret = password; setPassword(''); setConfirm(''); controller.submitLocal(name.trim(), username.trim(), secret, remote);},
    continueSetup: () => {if (chosen === 'local') controller.chooseKind('local'); else controller.submitHosted(name.trim(), {navigate: !signedIn, remote});}};
}
