import React from 'react';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button} from '../../../../kit/controls';
import {AccountEntryChoices, AccountEntryChoicesLegal, AccountEntryFootNote, AccountEntryNote, AccountEntryNotes, AccountEntryResend, AccountEntrySentNotes} from '../../../../kit/accounts/AccountEntryParts';
import {PasswordStrengthMeter} from '../../../../kit/accounts/PasswordStrengthMeter';
import {NativePorticoSignIn, porticoUsernameUnavailable} from '../../../../platform/portico-sign-in';
import {TVVerificationForm} from '../../../../tv/auth';
import {SignInPage} from '../../../touch/accounts/SignInPage';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders Portico Account sign-in, two-step verification, password reset, registration, email verify, provider completion, and contact verify stages. */
export function renderEntryPorticoAuthStage({view, kit}: EntryStageContext): React.ReactElement | null | undefined {
  const {
    preparedAuthSource, setProviderLoading, theme, stage, setStage, busy, setError,
    username, setUsername, password, setPassword, code, setCode,
    usernameUnavailable, setUsernameUnavailable, registerOpening, verifyOpening, setVerifyOpening, verifyOpeningRef,
    providerOpening, providerUsernameUnavailable, setProviderUsernameUnavailable, factorOpening,
    acceptTerms, name, setName, email, setEmail, masked, setMasked,
    invitationIntent, invitationSwitch, options, transaction, setTransaction,
    recovery, setRecovery, recoveryCode, setRecoveryCode,
    memberFactorOperation, stopMemberMove,
    lifetime, operation, account, busyRef, stageRef, registerOpeningRef, providerOpeningRef, registerDraftRev,
    contact, factorKind, acting, run, back, consume, directory, intentRunner, setProviderNotice,
    requestReset, submitTVFactor, action, tvDevice, openLegalLink, providerOffered,
  } = view;
  const {field, codeField, twoStepField, fields, ageTerms, status, providerNoticeView, layout, legalFooter} = kit;

  if (tvDevice && stage === 'factor') {
    return <TVVerificationForm kind={factorKind.current} code={code} onCodeChange={setCode}
      recovery={recovery} recoveryCode={recoveryCode} onRecoveryCodeChange={setRecoveryCode}
      onRecoveryChange={value => {setRecovery(value); setCode(''); setRecoveryCode(''); setError(undefined);}}
      busy={busy} error={status} onSubmit={value => {void run(() => submitTVFactor(value));}} onBack={back}/>;
  }

  if (stage === 'factor' && factorKind.current === 'portico' && !tvDevice) {
    // The phone Portico factor form acts only through its own opening: the exact issued
    // witness, source, challenge and the draft revision it renders.
    const opening = factorOpening, revision = registerDraftRev.current, entered = recovery ? recoveryCode : code;
    const formLive = () => lifetime.current && stageRef.current === 'factor' && !!opening && opening.owner.live
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    return layout(
      t('auth.enterVerificationCode'),
      t('services.signin.twoStep'),
      fields(recovery ? field(t('services.signin.recovery'), entered, edit(setRecoveryCode)) : twoStepField(entered, edit(setCode))),
      action('factor', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !entered || !formLive() || source.factorChallengeIdentity() !== opening.witness) return;
        try {
          await source.factor(entered, {signal: opening.life.signal, isCurrent: formLive}, opening.witness);
        } catch (refusal) {
          if (!formLive()) return;
          throw refusal;
        }
        if (!formLive()) return;
        setCode(''); setRecoveryCode(''); setRecovery(false);
        await directory();
      }, !entered),
      recovery ? undefined : <Button variant="link" disabled={busy} onPress={() => {if (!formLive()) return; registerDraftRev.current++; setRecoveryCode(''); setRecovery(true);}}>{`${t('services.signin.lostPhone')} ${t('services.signin.recovery')}`}</Button>,
    );
  }

  if (stage === 'factor') {
    return layout(
      t('auth.enterVerificationCode'),
      undefined,
      fields(twoStepField(code, setCode)),
      action('factor', t('action.continue'), async () => {
        if (memberFactorOperation.current && memberFactorOperation.current === operation.current) {
          const result = await memberFactorOperation.current.completeMemberMoveFactor(code);
          memberFactorOperation.current = undefined; stopMemberMove(); consume(result);
        } else {
          consume(await (operation.current!.directoryChallengePending()
            ? operation.current!.completeDirectorySecondFactor(code)
            : operation.current!.completeSecondFactor(code)));
        }
        setCode('');
      }, !code),
    );
  }

  const authOptions = account.current && preparedAuthSource.current === account.current ? options : undefined;
  const availableProviders = authOptions?.providers.filter(providerOffered) ?? [];

  if (stage === 'portico' && !theme.isTV) {
    const source = account.current, ticket = invitationSwitch.current;
    const current = () => !!source && lifetime.current && stageRef.current === 'portico' && account.current === source
      && preparedAuthSource.current === source && invitationSwitch.current === ticket
      && (!ticket || (ticket.active && invitationIntent.current === ticket.entry));
    // Apple and Google are always offered; one Services has not turned on, or this device can't open, says so plainly.
    const onProvider = (id: 'apple' | 'google') => {
      if (!current() || busy) return;
      if (!availableProviders.some(value => value.id === id)) {setProviderNotice(id); return;}
      setProviderNotice(undefined); void run(async () => {
        if (!current() || !source) return;
        setProviderLoading(source);
        try {
          const tx = await source.provider(id, current); if (!current()) return; setTransaction(tx);
          if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {
            const caller = new AbortController(); await source.finishProvider(undefined, undefined, undefined, {signal: caller.signal, isCurrent: current});
            if (current()) await directory();
          } else if (current()) setStage('provider');
        } catch (err) {if (current()) throw err;}
        finally {if (lifetime.current) setProviderLoading(value => value === source ? undefined : value);}
      });
    };
    const onContinue = () => {
      if (!current() || busy || !authOptions?.password || !username || !password || !source) return;
      void run(async () => {
        if (!current()) return;
        try {await source.password(username, password, current); if (current()) await directory();}
        catch (err) {if (current()) throw err;}
      });
    };
    const onForgot = () => {if (!current() || busy || !authOptions?.recovery) return; void run(requestReset);};
    const onCreate = () => {if (!current() || busy || !authOptions?.registration) return; setPassword(''); setStage('register');};
    intentRunner.current = intent => {
      if (intent.kind === 'provider') onProvider(intent.id);
      else if (intent.kind === 'continue') onContinue();
      else if (intent.kind === 'forgot') onForgot();
      else onCreate();
    };
    return <SignInPage busy={busy} error={status ?? providerNoticeView} onOpenLink={openLegalLink}
      passwordEnabled={!!authOptions?.password} registration={!!authOptions?.registration} recovery={authOptions?.recovery !== false}
      username={username} password={password} onUsernameChange={value => {if (current()) setUsername(value);}} onPasswordChange={value => {if (current()) setPassword(value);}}
      onProvider={onProvider} onContinue={onContinue} onForgot={onForgot} onCreate={onCreate}
      onLocal={() => {if (!current() || busy) return; setPassword(''); setProviderNotice(undefined); setStage('address');}}/>;
  }

  if (stage === 'portico') {
    return layout(
      t('accounts.signIn.portico'),
      t('auth.accountLead'),
      options?.password ? fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username'), field(t('auth.password'), password, setPassword, true, 'password')) : undefined,
      options?.password ? action('account-password', t('auth.signIn'), async () => {await account.current!.password(username, password); await directory();}, !username || !password) : undefined,
      <AccountEntryChoices>
        {options?.providers.filter(providerOffered).map(value => <Button key={value.id} variant="glass" disabled={busy} onPress={() => {void run(async () => {
          const tx = await account.current!.provider(value.id); setTransaction(tx);
          if (tx.status === 'verified' && !tx.needsUsername && !tx.mfaRequired) {await account.current!.finishProvider(); await directory();} else setStage('provider');
        });}}>{t(value.id === 'google' ? 'auth.signInWithGoogle' : 'auth.signInWithApple')}</Button>)}
        {options?.registration && <Button variant="link" disabled={busy} onPress={() => {setPassword(''); setStage('register');}}>{t('auth.createAccount')}</Button>}
        {options?.recovery !== false && <Button variant="link" disabled={busy} onPress={() => {void run(requestReset);}}>{t('auth.forgotPassword')}</Button>}
        <Button variant="link" disabled={busy} onPress={() => {setPassword(''); setStage('address');}}>{t('accounts.signIn.local')}</Button>
        <AccountEntryChoicesLegal onOpenLink={openLegalLink}/>
      </AccountEntryChoices>,
    );
  }

  if (stage === 'forgot') {
    return layout(
      t('auth.forgotPasswordTitle'),
      t('auth.resetLinkLede'),
      fields(field(t('auth.usernameOrEmail'), username, setUsername, false, 'username')),
      action('request-reset', t('auth.resetLinkAction'), requestReset, !username.trim()),
      <AccountEntryNotes>
        <AccountEntryNote>{t('auth.privacySameAnswer')}</AccountEntryNote>
        {theme.isTV ? null : <AccountEntryNote>{t('auth.noPasswordForSocial')}</AccountEntryNote>}
      </AccountEntryNotes>,
    );
  }

  if (stage === 'forgot-sent') {
    return layout(
      t('auth.checkEmail'),
      t('auth.resetLinkSent'),
      <AccountEntrySentNotes expiry={t('services.recovery.expiry')} privacy={t('auth.privacySameAnswer')}/>,
      action('back-to-sign-in', t('auth.backToSignIn'), async () => {if (lifetime.current) setStage('portico');}),
      <AccountEntryFootNote>{t('auth.noPasswordForSocial')}</AccountEntryFootNote>,
    );
  }

  if (stage === 'register') {
    const opening = registerOpening, revision = registerDraftRev.current, submitted = {username, name, email};
    const live = (source: NativePorticoSignIn) => lifetime.current && stageRef.current === 'register' && !!opening
      && opening.owner.live && registerOpeningRef.current === opening && opening.source === source && account.current === source
      && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    const usernameField = field(t('auth.username'), username, next => {setUsernameUnavailable(false); edit(setUsername)(next);}, false, 'username',
      !theme.isTV && usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, theme.isTV ? undefined : t('auth.usernameHelp'));
    const nameField = theme.isTV ? field(t('auth.yourName'), name, edit(setName))
      : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'));
    const emailField = field(t('auth.email'), email, edit(setEmail), false, 'email');
    return layout(
      t('auth.createYourAccount'),
      theme.isTV ? undefined : t('auth.oneAccount'),
      fields(
        ...(theme.isTV ? [usernameField, nameField, emailField] : [emailField, usernameField, nameField]),
        field(t('auth.password'), password, edit(setPassword), true, 'new-password'),
        !theme.isTV && <PasswordStrengthMeter key="strength" password={password}/>,
        ageTerms,
      ),
      action('register', t(theme.isTV ? 'auth.createAccount' : 'action.continue'), async () => {
        const source = opening?.source;
        if (!source || !live(source)) return;
        try {
          const value = await source.register(submitted.username, theme.isTV ? submitted.name : submitted.name.trim() || submitted.username.trim(), password, submitted.email, acceptTerms);
          if (!live(source)) return;
          setVerifyOpening(Object.freeze({source, registration: value})); setPassword(''); setMasked(value.maskedAddress); setStage('verify');
        } catch (refusal) {
          if (!live(source)) return;
          if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, !acceptTerms || (theme.isTV ? (!username || !name || !password || !email) : (!username || !password || !email))),
      theme.isTV ? undefined : legalFooter,
    );
  }

  if (stage === 'verify') {
    const opening = verifyOpening, revision = registerDraftRev.current;
    const live = () => !!opening && lifetime.current && stageRef.current === 'verify' && verifyOpeningRef.current === opening
      && account.current === opening.source && registerDraftRev.current === revision;
    const verified = opening?.registration.contactVerified === true;
    const edit = (apply: (next: string) => void) => (next: string) => {if (!live()) return; registerDraftRev.current++; apply(next);};
    return layout(
      verified ? t('auth.chooseUsername') : t('auth.checkEmail'),
      verified ? undefined : t('auth.codeSentTo', {address: masked}),
      verified ? fields(
        field(t('auth.username'), username, next => {if (!live()) return; setUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
          usernameUnavailable ? t('web.signIn.error.accountTaken') : undefined, t('auth.usernameHelp')),
        field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername')),
      ) : fields(codeField(t('auth.verificationCode'), code, edit(setCode))),
      action('verify', t(theme.isTV || verified ? 'action.continue' : 'auth.createAccount'), async () => {
        if (!opening || !live()) return;
        try {
          await opening.source.verifyRegistration(verified ? '' : code, opening.registration, {isCurrent: live}, verified ? {username, displayName: name} : undefined);
          if (live()) await directory();
        } catch (refusal) {
          if (!live()) return;
          const fresh = porticoUsernameUnavailable(refusal) ? opening.source.registrationAfterCollision(opening.registration) : undefined;
          if (fresh) {setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setUsernameUnavailable(true); return;}
          throw refusal;
        }
      }, verified ? username.trim().length < 3 : !code),
      verified || theme.isTV ? undefined : <AccountEntryResend help={t('services.signup.codeHelp')} action={
        <Button testID="accounts-registration-resend" variant="link" disabled={busy} onPress={() => {
          if (!opening || !live() || busyRef.current || acting.current) return;
          void run(async () => {
            if (!live()) return;
            try {
              const fresh = await opening.source.resendRegistration(opening.registration, {isCurrent: live});
              if (!live()) return;
              setVerifyOpening(Object.freeze({source: opening.source, registration: fresh})); setCode(''); setMasked(fresh.maskedAddress);
            } catch (err) {if (live()) throw err;}
          });
        }}>{t('auth.sendCodeAgain')}</Button>}/>,
    );
  }

  if (stage === 'provider') {
    const opening = providerOpening, tx = transaction, revision = registerDraftRev.current;
    const live = () => lifetime.current && stageRef.current === 'provider' && !!opening && !!tx
      && opening.owner.live && providerOpeningRef.current === opening && opening.transaction === tx
      && account.current === opening.source && registerDraftRev.current === revision;
    const edit = (apply: (next: string) => void) => (next: string) => {registerDraftRev.current++; apply(next);};
    const providerName = tx?.provider === 'apple' ? t('accounts.provider.apple') : tx?.provider === 'google' ? t('accounts.provider.google') : undefined;
    const phoneStep = !!tx?.needsUsername && !theme.isTV;
    const heading = phoneStep ? t('auth.chooseUsername') : t('accounts.signIn.portico');
    const lede = phoneStep ? t('services.provider.lastThing') : undefined;
    const continuing = tx?.needsUsername && providerName && !theme.isTV
      ? <AccountEntryNote>{t('services.provider.heading', {provider: providerName})}</AccountEntryNote> : undefined;
    const sharedAddress = !theme.isTV && !!tx?.verifiedEmail && tx.needsUsername
      ? <AccountEntryNote selectable>{`${t('auth.email')}: ${tx.verifiedEmail}`}</AccountEntryNote> : undefined;
    return layout(
      heading,
      lede,
      fields(
        continuing,
        sharedAddress,
        tx?.needsUsername && field(t('auth.username'), username, next => {setProviderUsernameUnavailable(false); edit(setUsername)(next);}, false, undefined,
          !theme.isTV && providerUsernameUnavailable ? t('web.signIn.error.accountTaken') : undefined,
          theme.isTV ? undefined : t('services.provider.usernameHelp')),
        tx?.needsUsername && (theme.isTV ? field(t('auth.yourName'), name, edit(setName))
          : field(`${t('auth.yourName')} · ${t('field.optional')}`, name, edit(setName), false, undefined, undefined, undefined, t('services.signup.sameUsername'))),
        tx?.needsUsername && ageTerms,
        tx?.mfaRequired && codeField(t('auth.verificationCode'), code, edit(setCode)),
        !tx?.contactVerified && field(t('auth.email'), email, edit(setEmail)),
      ),
      action('provider-complete', t('action.continue'), async () => {
        const source = opening?.source;
        if (!opening || !source || !live()) return;
        if (!tx?.contactVerified) {
          const challenge = await source.sendContact(email);
          if (!live()) return;
          contact.current = challenge.challengeId; setMasked(challenge.maskedAddress); setCode(''); setStage('contact');
        } else {
          try {
            await source.finishProvider(theme.isTV ? username || undefined : username.trim() || undefined,
              theme.isTV ? name || undefined : name.trim() || username.trim() || undefined, code || undefined,
              {signal: opening.life.signal, isCurrent: live}, !!tx?.needsUsername && acceptTerms);
          } catch (refusal) {
            if (!live()) return;
            if (!theme.isTV && porticoUsernameUnavailable(refusal)) {setProviderUsernameUnavailable(true); return;}
            throw refusal;
          }
          if (!live()) return;
          await directory();
        }
      }, !!tx?.needsUsername && !acceptTerms),
      tx?.needsUsername && !theme.isTV ? legalFooter : undefined,
    );
  }

  if (stage === 'contact') {
    return layout(
      t('auth.checkEmail'),
      t('auth.codeSentTo', {address: masked}),
      fields(codeField(t('auth.verificationCode'), code, setCode)),
      action('contact', t('action.continue'), async () => {
        await account.current!.verifyContact(contact.current!, code);
        setTransaction(value => value && {...value, contactVerified: true});
        setCode(''); setStage('provider');
      }, !code),
    );
  }

  return undefined;
}
