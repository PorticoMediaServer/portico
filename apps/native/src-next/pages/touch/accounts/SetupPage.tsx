import React from 'react';
import type {SetupController} from '../../../../../../packages/client-core/src/setup-controller';
import {setupBusyMessage, setupEndedMessage, setupErrorMessage, setupLockoutReason, setupNewerMessage} from '../../../../../../packages/client-core/src/presentation/setup-messages';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {StateLayout} from '../../../kit/states';
import {PasswordStrengthMeter} from '../../../kit/accounts/PasswordStrengthMeter';
import {AccountForm} from '../../../kit/accounts/AccountForm';
import {PhoneSetupApproval} from '../../../kit/accounts/PhoneSetupApproval';
import {SetupCaption, SetupChoice, SetupChoices, SetupCodeFolders, SetupField, SetupFields, SetupSegment, SetupSegments, StartOverSheet} from '../../../kit/accounts/SetupParts';
import type {NativeSetupTarget} from '../../../platform/setup-adapter';
import {useSetupController, useSetupStates} from '../../shared/accounts/useNativeSetup';

/** First-run setup in the phone app (auth.html frame 1p, B-DESIGN §6) on the shared setup controller. The server decides
 * every state; this draws it. A Portico Account signed in to the app approves here (label 28 to 29); otherwise the
 * approval page opens in the browser and setup carries on when the person comes back (label 30). The only way out is the
 * ordinary Back (Justin, 31 Oct). Once the owner is admitted, the first library is the shell's `PhoneFirstLibrary`. */
export function SetupPage({target, onAdmitted, onLeave}: {target: NativeSetupTarget; onAdmitted: (serverId: string) => void; onLeave: () => void}) {
  const controller = useSetupController(target);
  return controller ? <SetupStates controller={controller} target={target} onAdmitted={onAdmitted} onLeave={onLeave}/> : <Waiting/>;
}

const codeFolders = ['web.setup.codeFolderWindows', 'web.setup.codeFolderLinuxPackage', 'web.setup.codeFolderMac', 'web.setup.codeFolderDocker', 'web.setup.codeFolderByHand', 'web.setup.codeFolderLog'] as const;

function SetupStates({controller, target, onAdmitted, onLeave}: {controller: SetupController; target: NativeSetupTarget; onAdmitted: (serverId: string) => void; onLeave: () => void}) {
  const t = defaultI18n.t, setup = useSetupStates({controller, target, onAdmitted, onLeave}), {view, busy, name} = setup;
  const back = {id: 'back', label: t('action.back'), disabled: false, onPress: setup.back};
  const tryAgain = (onPress = setup.retry) => ({id: 'retry', label: t('action.tryAgain'), disabled: busy, loading: busy, onPress});
  const Layout = setup.phone ? AccountForm : StateLayout;
  const message = (title: string, body: string, primary?: ReturnType<typeof tryAgain>, secondary: ReturnType<typeof tryAgain> | typeof back = back) =>
    <>
      <Layout testID="accounts-setup" title={title} body={body} primaryAction={primary} secondaryAction={secondary}/>
      <StartOverSheet open={setup.startingOver} onOpenChange={setup.setStartingOver} title={t('web.setup.startOverTitle')} description={t('web.setup.startOverBody')}
        cancelLabel={t('action.cancel')} confirmLabel={t('web.setup.startOver')} onCancel={() => setup.setStartingOver(false)} onConfirm={setup.confirmStartOver}/>
    </>;
  const startOver = {id: 'start-over', label: t('web.setup.startOver'), disabled: busy, loading: busy && view.kind === 'starting_over', onPress: setup.startOver};

  switch (view.kind) {
    case 'connecting': case 'admitting': case 'renewing': case 'library': case 'done': case 'starting_over':
      return <Waiting/>;
    case 'finishing':
      return <Layout testID="accounts-setup" title={t('web.claim.finishingOnServer')} body={view.slow ? t('web.claim.finishingSlow') : undefined}/>;
    case 'exhausted':
      return message(t('web.claim.finishingOnServer'), t('web.claim.finishingSlow'), tryAgain(), startOver);
    case 'refused':
      return message(t('accounts.setup.title'), view.reason === 'already_set_up' ? setupErrorMessage({code: 'setup_complete', retryable: false})
        : t(view.reason === 'not_local_source' ? 'web.setup.notLocalSource' : 'web.setup.notOnNetwork'));
    case 'busy_elsewhere':
      return message(t('accounts.setup.title'), setupBusyMessage(view.retryAt), startOver);
    case 'ended':
      return setup.offer.offer ? message(t('accounts.setup.title'), setupEndedMessage(view.outcome, view.name || name), startOver)
        : message(t('accounts.setup.declinedTitle', {server: view.name || name}), setupEndedMessage(view.outcome, view.name || name), tryAgain(setup.startAgain));
    case 'failed':
      if (setup.offer.offer) return view.retryable ? message(t('accounts.setup.title'), setupErrorMessage(view.error), tryAgain(), startOver) : message(t('accounts.setup.title'), setupErrorMessage(view.error), startOver);
      return message(t('accounts.setup.title'), setupErrorMessage(view.error), view.retryable ? tryAgain() : undefined);
    case 'code':
      // From outside the server's network: the setup code from its log (auth.html 1e panels 1–2).
      return <Layout testID="accounts-setup" title={t('web.setup.codeTitle')} body={view.invalid ? t('web.setup.codeInvalid') : t('web.setup.codeHint')}
        primaryAction={{id: 'continue', label: t('accounts.signIn.continue'), disabled: busy || !setup.setupCode.trim(), loading: busy, onPress: setup.submitCode}}
        secondaryAction={back}
        beforeActions={<SetupFields><SetupField label={t('web.setup.codeLabel')} value={setup.setupCode} onChange={setup.setSetupCode} editable={!busy}/>
          <SetupCodeFolders lines={codeFolders.map(key => t(key))}/></SetupFields>}/>;
    case 'lockout': {
      // The address in use won't work once setup is complete (auth.html 1e panel 5): only the fixes that would let it in,
      // and always a quiet Back, to choose again (on its own where no fix applies: Remote access leaves this network out).
      const keep = {id: 'keep-allowing', label: t('web.setup.keepAllowing'), disabled: busy, onPress: () => setup.lockoutFix('address')};
      const standard = {id: 'switch-standard', label: t('web.setup.switchStandard'), disabled: busy, onPress: () => setup.lockoutFix('standard')};
      const fixes = [...(view.fixes.includes('address') ? [keep] : []), ...(view.fixes.includes('standard') ? [standard] : [])];
      return <Layout testID="accounts-setup" title={t('web.setup.lockoutTitle')} body={setupLockoutReason(view.reason)}
        primaryAction={fixes[0]} secondaryAction={fixes[1]} detailsAction={back} detailsPresentation="link"/>;
    }
    case 'unknown':
      return message(t('accounts.setup.title'), setupNewerMessage(), tryAgain());
    case 'awaiting_approval': {
      // The Portico Account declined the last request: one line, then Continue (a new request) and Back.
      if (view.declined) return <Layout testID="accounts-setup" title={t('accounts.setup.title')} body={t('web.setup.declined')}
        primaryAction={{id: 'continue', label: t('accounts.signIn.handoff'), disabled: busy, loading: busy, onPress: setup.retry}} secondaryAction={back}/>;
      // Label 29: the app's own Portico Account approves here, with no trip. Not now is the ordinary Back.
      if (setup.signedIn && !setup.notNow) return <PhoneSetupApproval busy={busy} onClose={setup.notNowPressed} onApprove={setup.approveInApp}
        display={{serverName: view.name || name, address: target.origin, accountName: setup.accountName}}/>;
      const link = view.approvalUrl;
      return <Layout testID="accounts-setup" title={t('web.claim.title', {server: view.name || name})} body={t('accounts.setup.waiting')}
        primaryAction={link ? {id: 'open', label: t('accounts.signIn.continuePortico'), disabled: busy, onPress: () => setup.openApproval(link)} : tryAgain()}
        secondaryAction={back}/>;
    }
    case 'form': case 'starting': {
      const starting = view.kind === 'starting';
      if (view.mode === 'local') {
        return <Layout testID="accounts-setup" title={t('web.setup.ownerTitle')} body={t('web.setup.localSignInBody')}
          beforeActions={<SetupFields>
            <SetupField label={t('auth.username')} value={setup.username} onChange={setup.setUsername} credential="username" editable={!busy}/>
            <SetupField label={t('auth.password')} value={setup.password} onChange={setup.setPassword} credential="new-password" editable={!busy}/>
            <PasswordStrengthMeter password={setup.password}/>
            <SetupField label={t('accounts.signIn.confirmPassword')} value={setup.confirm} onChange={setup.setConfirm} credential="new-password" editable={!busy}/>
            <SetupCaption>{t('accounts.setup.localRecovery')}</SetupCaption></SetupFields>}
          primaryAction={{id: 'create-owner', label: t('web.setup.createOwner'), disabled: busy || starting || !setup.localValid, loading: busy || starting, onPress: setup.createOwner}}
          secondaryAction={back}/>;
      }
      return <Layout testID="accounts-setup" title={t('accounts.setup.title')} body={t('accounts.setup.at', {address: new URL(target.origin).host})}
        beforeActions={<SetupFields>
          <SetupField label={t('web.general.serverName')} value={name} onChange={setup.setName} editable={!busy}/>
          <SetupCaption>{t('web.setup.serverNameHelp')}</SetupCaption><SetupCaption>{t('web.setup.howSignIn')}</SetupCaption>
          <SetupChoices>{(['hosted', 'local'] as const).filter(mode => mode === 'local' || target.server.hostedConfigured).map(mode =>
            <SetupChoice key={mode} checked={setup.chosen === mode} disabled={busy || starting} onPress={() => setup.setChosen(mode)}
              title={t(mode === 'hosted' ? 'accounts.settings.portico' : 'web.setup.localSignIn') + (mode === 'hosted' ? ' · ' + t('web.setup.recommended') : '')}
              body={t(mode === 'hosted' ? 'web.setup.porticoBody' : 'web.setup.localSignInBody')}/>)}</SetupChoices>
          <SetupCaption>{t('web.setup.remoteAccess')}</SetupCaption>
          <SetupSegments>{(['on', 'private', 'off'] as const).map(value =>
            <SetupSegment key={value} testID={`setup-remote-${value}`} checked={setup.remoteAccess === value} disabled={busy || starting} onPress={() => setup.setAccessChoice(value)}
              label={t(value === 'on' ? 'web.setup.remoteOn' : value === 'private' ? 'web.setup.remotePrivate' : 'web.setup.remoteOff')}/>)}</SetupSegments>
          <SetupCaption>{t(setup.remoteAccess === 'private' ? 'web.setup.remotePrivateHelp' : 'web.setup.remoteHomeHelp')}</SetupCaption>
          {setup.remoteAccess === 'on' && <SetupChoices>
            <SetupCaption>{t('web.setup.remoteSecurity')}</SetupCaption>
            {(['enhanced', 'standard'] as const).map(value =>
              <SetupChoice key={value} testID={`setup-security-${value}`} icon={value === 'enhanced' ? 'i-shield' : 'i-globe'} checked={setup.securityChoice === value}
                disabled={busy || starting} onPress={() => setup.setSecurityChoice(value)}
                title={t(value === 'enhanced' ? 'web.setup.enhanced' : 'web.setup.standard') + (value === 'enhanced' ? ' · ' + t('web.setup.recommended') : '')}
                body={t(value === 'enhanced' ? 'web.setup.enhancedBody' : 'web.setup.standardBody')}/>)}
          </SetupChoices>}</SetupFields>}
        primaryAction={{id: 'setup-continue', label: t('action.continue'), disabled: busy || starting || !name.trim(), loading: busy || starting, onPress: setup.continueSetup}}
        secondaryAction={back}/>;
    }
  }
}

function Waiting() {
  const t = defaultI18n.t;
  return <StateLayout testID="accounts-setup-waiting" title={t('web.setup.preparing')}/>;
}
