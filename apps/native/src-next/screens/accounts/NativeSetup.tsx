import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {AppState, Linking, Text, View} from 'react-native';
import {createSetupController, type SetupController} from '../../../../../packages/client-core/src/setup-controller';
import {setupBusyMessage, setupEndedMessage, setupErrorMessage, setupNewerMessage} from '../../../../../packages/client-core/src/presentation/setup-messages';
import {passwordStrength} from '../../../../../packages/client-core/src/password-strength';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable, TextField} from '../../kit/controls';
import {StateLayout} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import {nativeAccounts} from '../../platform/accounts';
import {createNativeSetupAdapter, type NativeSetupTarget} from '../../platform/setup-adapter';
import {PasswordStrengthMeter} from './PasswordStrengthMeter';
import {PhoneAccountForm} from './PhoneAccountForm';
import {PhoneSetupApproval} from './PhoneSetupApproval';

/** First-run setup in the phone app (auth.html frame 1p, B-DESIGN §6) on the shared setup controller. The server decides
 * every state; this draws it. A Portico Account signed in to the app approves here (label 28 to 29); otherwise the
 * approval page opens in the browser and setup carries on when the person comes back (label 30). The only way out is the
 * ordinary Back (Justin, 31 Oct). Once the owner is admitted, the first library is the shell's `PhoneFirstLibrary`. */
export function NativeSetup({target, onAdmitted, onLeave}: {target: NativeSetupTarget; onAdmitted: (serverId: string) => void; onLeave: () => void}) {
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
  return controller ? <SetupStates controller={controller} target={target} onAdmitted={onAdmitted} onLeave={onLeave}/> : <Waiting/>;
}

const noSubscription = () => () => {};
function SetupStates({controller, target, onAdmitted, onLeave}: {controller: SetupController; target: NativeSetupTarget; onAdmitted: (serverId: string) => void; onLeave: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const busy = useSyncExternalStore(controller.subscribe, controller.busy);
  const credentials = nativeAccounts.accountCredentials();
  const signedIn = useSyncExternalStore(credentials?.subscribe ?? noSubscription, () => !!credentials?.getSnapshot().session);
  const [name, setName] = useState(target.server.name);
  const [username, setUsername] = useState(''), [password, setPassword] = useState(''), [confirm, setConfirm] = useState('');
  const [notNow, setNotNow] = useState(false);
  const [chosen, setChosen] = useState<'hosted' | 'local'>(target.server.hostedConfigured ? 'hosted' : 'local');
  // Admitted: setup's own part is over, and the first library follows in the shell.
  useEffect(() => {if (view.kind === 'library' || view.kind === 'done') onAdmitted(target.server.id);}, [view.kind, onAdmitted, target.server.id]);
  useEffect(() => {if (view.kind !== 'awaiting_approval') setNotNow(false);}, [view.kind]);

  // Back in the form steps through it as on the web: the owner form back to the choice, the choice out of setup.
  const back = {id: 'back', label: t('action.back'), disabled: false, onPress: () => {
    if (view.kind !== 'form') controller.back();
    else if (view.mode === 'local') controller.chooseKind('choose');
    else onLeave();
  }};
  const tryAgain = (onPress = controller.retry) => ({id: 'retry', label: t('action.tryAgain'), disabled: busy, loading: busy, onPress});
  const Layout = theme.platform === 'phone' ? PhoneAccountForm : StateLayout;
  const message = (title: string, body: string, primary?: ReturnType<typeof tryAgain>) =>
    <Layout testID="accounts-setup" title={title} body={body} primaryAction={primary} secondaryAction={back}/>;
  const caption = (text: string, key?: string) => <Text key={key} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{text}</Text>;
  const field = (label: string, value: string, change: (next: string) => void, credential?: 'username' | 'new-password') =>
    <TextField key={label} label={label} value={value} onChangeText={change} appearance={theme.platform === 'phone' ? 'auth' : undefined}
      secureTextEntry={credential === 'new-password'} textContentType={credential === 'new-password' ? 'newPassword' : credential} autoComplete={credential} editable={!busy}/>;

  switch (view.kind) {
    case 'connecting': case 'admitting': case 'renewing': case 'library': case 'done':
      return <Waiting/>;
    case 'finishing':
      return <Layout testID="accounts-setup" title={t('web.claim.finishingOnServer')} body={view.slow ? t('web.claim.finishingSlow') : undefined}/>;
    case 'exhausted':
      return message(t('web.claim.finishingOnServer'), t('web.claim.finishingSlow'), tryAgain());
    case 'refused':
      return message(t('accounts.setup.title'), view.reason === 'already_set_up' ? setupErrorMessage({code: 'setup_complete', retryable: false})
        : t(view.reason === 'not_local_source' ? 'web.setup.notLocalSource' : 'web.setup.notOnNetwork'));
    case 'busy_elsewhere':
      return message(t('accounts.setup.title'), setupBusyMessage(view.retryAt));
    case 'ended':
      return message(t('accounts.setup.declinedTitle', {server: view.name || name}), setupEndedMessage(view.outcome, view.name || name), tryAgain(controller.startAgain));
    case 'failed':
      return message(t('accounts.setup.title'), setupErrorMessage(view.error), view.retryable ? tryAgain() : undefined);
    case 'unknown':
      return message(t('accounts.setup.title'), setupNewerMessage(), tryAgain());
    case 'awaiting_approval': {
      // The Portico Account declined the last request: one line, then Continue (a new request) and Back.
      if (view.declined) return <Layout testID="accounts-setup" title={t('accounts.setup.title')} body={t('web.setup.declined')}
        primaryAction={{id: 'continue', label: t('accounts.signIn.handoff'), disabled: busy, loading: busy, onPress: controller.retry}} secondaryAction={back}/>;
      // Label 29: the app's own Portico Account approves here, with no trip. Not now is the ordinary Back.
      if (signedIn && !notNow) return <PhoneSetupApproval busy={busy} onClose={() => setNotNow(true)} onApprove={controller.approveInApp}
        display={{serverName: view.name || name, address: target.origin, accountName: credentials?.getSnapshot().session?.account.displayName || credentials?.getSnapshot().session?.account.username || ''}}/>;
      const link = view.approvalUrl;
      return <Layout testID="accounts-setup" title={t('web.claim.title', {server: view.name || name})} body={t('accounts.setup.waiting')}
        primaryAction={link ? {id: 'open', label: t('accounts.signIn.continuePortico'), disabled: busy, onPress: () => {void Linking.openURL(link).catch(() => {});}} : tryAgain()}
        secondaryAction={back}/>;
    }
    case 'form': case 'starting': {
      const starting = view.kind === 'starting';
      if (view.mode === 'local') {
        const valid = !!name.trim() && /^[^\s@]{3,64}$/.test(username.trim()) && passwordStrength(password).acceptable && password === confirm;
        return <Layout testID="accounts-setup" title={t('web.setup.ownerTitle')} body={t('web.setup.localSignInBody')}
          beforeActions={<View style={{alignSelf: 'stretch', gap: 14, marginBottom: 20}}>
            {field(t('auth.username'), username, setUsername, 'username')}{field(t('auth.password'), password, setPassword, 'new-password')}
            <PasswordStrengthMeter key="strength" password={password}/>{field(t('accounts.signIn.confirmPassword'), confirm, setConfirm, 'new-password')}
            {caption(t('accounts.setup.localRecovery'), 'recovery')}</View>}
          primaryAction={{id: 'create-owner', label: t('web.setup.createOwner'), disabled: busy || starting || !valid, loading: busy || starting,
            onPress: () => {const secret = password; setPassword(''); setConfirm(''); controller.submitLocal(name.trim(), username.trim(), secret);}}}
          secondaryAction={back}/>;
      }
      return <Layout testID="accounts-setup" title={t('accounts.setup.title')} body={t('accounts.setup.at', {address: new URL(target.origin).host})}
        beforeActions={<View style={{alignSelf: 'stretch', gap: 14, marginBottom: 20}}>
          {field(t('web.general.serverName'), name, setName)}{caption(t('web.setup.serverNameHelp'), 'name-help')}{caption(t('web.setup.howSignIn'), 'how')}
          <View key="modes" style={{gap: 12}}>{(['hosted', 'local'] as const).filter(mode => mode === 'local' || target.server.hostedConfigured).map(mode =>
            <Focusable key={mode} accessibilityRole="radio" accessibilityState={{checked: chosen === mode}} disabled={busy || starting}
              onPress={() => setChosen(mode)} style={{padding: 14, borderWidth: 1, borderRadius: 14, borderColor: chosen === mode ? theme.colors.accentStrong : theme.colors.line}}>
              <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(mode === 'hosted' ? 'accounts.settings.portico' : 'web.setup.localSignIn')}{mode === 'hosted' ? ' · ' + t('web.setup.recommended') : ''}</Text>
              {caption(t(mode === 'hosted' ? 'web.setup.porticoBody' : 'web.setup.localSignInBody'))}
            </Focusable>)}</View></View>}
        primaryAction={{id: 'setup-continue', label: t('action.continue'), disabled: busy || starting || !name.trim(), loading: busy || starting,
          onPress: () => {if (chosen === 'local') controller.chooseKind('local'); else controller.submitHosted(name.trim(), {navigate: !signedIn});}}}
        secondaryAction={back}/>;
    }
  }
}

function Waiting() {
  const t = defaultI18n.t;
  return <StateLayout testID="accounts-setup-waiting" title={t('web.setup.preparing')}/>;
}

