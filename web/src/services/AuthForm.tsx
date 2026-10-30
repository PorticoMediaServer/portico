import {useEffect, useLayoutEffect, useCallback, useMemo, useRef, useState, useSyncExternalStore, type ReactNode, type FormEvent} from 'react';
import {passwordStrength} from '@core/password-strength.ts';
import {AuthCodeBoxes, AuthLinks, AuthPasswordField} from '../kit/accounts';
import {HostedError, ServicesActions, ServicesAuthStage, ServicesBackAction, ServicesDivider, ServicesHeading, ServicesHint, ServicesInvitationCredentials, ServicesLabel, ServicesLead, ServicesNotice, ServicesPair, ServicesProviderAccount, ServicesProviderButton, ServicesProviderContext, ServicesProviderReturnHint, ServicesQuiet, ServicesRegistrationLead, ServicesStateIcon, ServicesStrength, ServicesTerms, ServicesVerificationCode} from '../kit/accounts/ServicesAuth';
import {Button, TextField} from '../kit/controls';
import {ServicesAuthFlow} from './auth-flow';
import {useAccountResource} from './use-account-resource';
import type {ServicesAccount} from './session';
import {servicesI18n} from './i18n';
import styles from './services.module.css';
import signInStyles from './services-signin-r46.module.css';
import {DocumentBootReady} from '../shell/DocumentBootReady';
import {ProviderLoading} from './ProviderLoading';

type Props = {providerFailure?: 'google' | 'apple' | 'unknown'; account: ServicesAccount; onSignedIn: (gesture?: boolean) => void; initialMode?: 'signin' | 'create' | 'forgot'; initialUsername?: string; embedded?: boolean; invitation?: boolean; heading?: ReactNode; lead?: ReactNode; before?: ReactNode; after?: ReactNode};
export function ServicesAuthForm(props: Props) {
  const {account, initialMode = 'signin', providerFailure} = props;
  const complete = useRef(props.onSignedIn); complete.current = props.onSignedIn;
  const create = useCallback(() => new ServicesAuthFlow(account, gesture => complete.current(gesture), initialMode, undefined, providerFailure), [account, initialMode, providerFailure]);
  const bound = useAccountResource(create);
  if (!bound) return props.embedded ? null : <ProviderLoading />;
  return <MountedAuthForm key={bound.id} {...props} flow={bound.value} />;
}

function MountedAuthForm({account, flow, initialUsername = '', embedded = false, invitation = false, heading, lead, before, after}: Props & {flow: ServicesAuthFlow}) {
  const {t} = useMemo(servicesI18n, []);
  const state = useSyncExternalStore(flow.subscribe, flow.getSnapshot);
  // Follow family replacement without copying a second session into the form.
  useSyncExternalStore(account.service.subscribe, account.service.getSnapshot);
  const readyAccount = flow.readyAccount();
  const [username, setUsername] = useState(initialUsername), [password, setPassword] = useState(''), [email, setEmail] = useState(''), [name, setName] = useState(''), [code, setCode] = useState('');
  // The exact submitted signup username, so a taken-name indication clears once
  // the person changes the field rather than on every keystroke request.
  const [submittedUsername, setSubmittedUsername] = useState('');
  const [clock, setClock] = useState(Date.now);
  useEffect(() => {
    if (!state.retryAt) return;
    setClock(Date.now());
    const timer = setInterval(() => setClock(Date.now()), 250);
    return () => clearInterval(timer);
  }, [state.retryAt]);
  useLayoutEffect(() => { void flow.initialize(); }, [flow]);
  useEffect(() => {
    const returned = (event: PageTransitionEvent) => {if (event.persisted) flow.resumeProviderNavigation();};
    globalThis.addEventListener?.('pageshow', returned);
    return () => globalThis.removeEventListener?.('pageshow', returned);
  }, [flow]);
  useEffect(() => { setPassword(''); setCode(''); if (state.mode === 'provider') setName(state.suggestedName ?? ''); }, [state.mode, state.suggestedName]);
  const submit = (work: () => Promise<void>) => (event: FormEvent) => { event.preventDefault(); void work(); };
  const retrySeconds = Math.max(0, Math.ceil((state.retryAt - clock) / 1000));
  const retryLabel = t('services.signin.tryIn', {time: `${Math.floor(retrySeconds / 60)}:${String(retrySeconds % 60).padStart(2, '0')}`});
  const busy = state.busy, blocked = busy || retrySeconds > 0, mode = state.mode;
  if (state.initializing || state.providerNavigating) return embedded ? null : <ProviderLoading />;
  // Closed Hosted codes that name a drawn field or state, so their sentence goes
  // there instead of the generic notice. Anything else keeps the generic notice.
  const errorCode = (state.error as {code?: string} | undefined)?.code;
  const codeRejected = mode === 'verify' && errorCode === 'challenge_invalid';
  const mailDown = mode === 'create' && errorCode === 'mail_unavailable';
  const chooseRegistrationName = mode === 'verify' && state.registration?.contactVerified === true;
  const nameTaken = (mode === 'create' || mode === 'provider' || chooseRegistrationName) && errorCode === 'username_unavailable' && submittedUsername !== '' && username.trim().toLowerCase() === submittedUsername;
  const back = <button type="button" disabled={busy} onClick={() => flow.go('signin')}>{t('auth.backToSignIn')}</button>;
  // Frame 7 draws the create form's way back with the same left arrow the provider
  // screen's Cancel uses; the code and reset screens draw it as plain text.
  const backAction = <button type="button" disabled={busy} onClick={() => flow.go('signin')}><ServicesBackAction>{t('auth.backToSignIn')}</ServicesBackAction></button>;
  const title = mode === 'create' ? t('auth.createYourAccount') : chooseRegistrationName ? t('auth.chooseUsername') : mode === 'verify' || mode === 'sent' ? t('auth.checkEmail') : mode === 'forgot' ? t('auth.forgotPasswordTitle') : mode === 'factor' ? t('auth.enterVerificationCode') : mode === 'recovery' ? t('services.signin.recovery') : mode === 'provider' ? t('auth.chooseUsername') : mode === 'ready' ? t('services.provider.ready') : heading ?? t('auth.welcomeHome');
  const body = chooseRegistrationName ? t('services.provider.lastThing') : mode === 'create' ? t('auth.oneAccount') : mode === 'verify' && state.registration ? t('auth.codeSentTo', {address: state.registration.maskedAddress}) : mode === 'sent' ? t('auth.resetLinkSent') : mode === 'forgot' ? t('auth.resetLinkLede') : mode === 'factor' ? t('services.signin.twoStep') : mode === 'recovery' ? t('services.signin.recoveryBody') : mode === 'provider' ? t('services.provider.lastThing') : mode === 'ready' ? t('services.provider.readyBody') : lead ?? t('auth.accountLead');
  const providers = state.options?.providers.filter(item => item.enabled).sort((a, b) => a.id.localeCompare(b.id)) ?? [];
  // auth5 draws the full sign-in page's own labels ("Sign in with"), distinct from
  // the create and invitation pages' "Continue with" wording in account/connect.
  const signIn = mode === 'signin' && !embedded && !invitation;
  const providerButtons = <>{providers.map(item => <ServicesProviderButton key={item.id} provider={item.id} disabled={busy} onClick={() => void flow.provider(item.id)}>{t(item.id === 'apple' ? (signIn ? 'auth.signInWithApple' : 'services.provider.apple') : (signIn ? 'auth.signInWithGoogle' : 'services.provider.google'))}</ServicesProviderButton>)}{providers.length > 0 && state.options?.password && <div className={mode === 'signin' ? signInStyles.divider : undefined}><ServicesDivider>{t(mode === 'create' ? 'services.provider.createDivider' : 'auth.orWithEmail')}</ServicesDivider></div>}</>;
  const content = <>
    {mode === 'verify' && !chooseRegistrationName && <ServicesStateIcon tone="info" icon="i-mail" size={32} />}
    {mode === 'sent' && <ServicesStateIcon tone="info" icon="i-mail" size={26} />}
    {mode === 'ready' && <ServicesStateIcon />}
    {mode === 'provider' && state.provider && <ServicesProviderContext provider={state.provider} email={state.verifiedEmail} />}
    {!(invitation && mode === 'signin') && <><ServicesHeading singleLine phoneAccountForm={mode === 'forgot' || mode === 'create' && !nameTaken}>{title}</ServicesHeading>{mode === 'verify' && state.registration && !chooseRegistrationName ? <ServicesRegistrationLead address={state.registration.maskedAddress} /> : mode !== 'provider-stopped' && <ServicesLead>{body}</ServicesLead>}</>}
    {mode === 'signin' && before}
    {mode === 'provider-stopped' && state.provider && <>
      <ServicesNotice icon="i-clock">{t('services.provider.stopped', {provider: t(state.provider === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}</ServicesNotice>
      {providers.some(item => item.id === state.provider) && <ServicesProviderButton provider={state.provider} disabled={blocked} onClick={() => {if (flow.getSnapshot() === state && state.provider) void flow.provider(state.provider);}}>{t(state.provider === 'apple' ? 'services.provider.apple' : 'services.provider.google')}</ServicesProviderButton>}
      {state.options?.password && <ServicesActions><Button disabled={busy} onClick={() => {if (flow.getSnapshot() === state) flow.go('signin');}}>{t('services.provider.useEmail')}</Button></ServicesActions>}
    </>}
    {(mode === 'signin' || mode === 'create') && providerButtons}
    {state.error && !codeRejected && !(errorCode === 'username_unavailable' && (mode === 'create' || mode === 'provider' || chooseRegistrationName)) ? state.retryAt > 0 ? <ServicesNotice tone="error">{t('services.signin.tooMany')}</ServicesNotice> : errorCode === 'invalid_credentials' ? <ServicesNotice tone="error">{t('services.signin.credentials')}</ServicesNotice> : mailDown ? <ServicesNotice icon="i-clock">{t('services.signup.mailUnavailable')}</ServicesNotice> : <HostedError error={state.error} /> : null}
    {!state.options && state.optionsFailed && <ServicesActions>
      <Button loading={busy} disabled={blocked} onClick={() => {if (flow.getSnapshot() === state) void flow.retryInitialization();}}>{retrySeconds > 0 ? retryLabel : t('action.tryAgain')}</Button>
    </ServicesActions>}
    {mode === 'signin' && state.options?.password && <>
      <form className={styles.form} onSubmit={submit(() => flow.signIn(username, password, code))}>
        <TextField variant="auth" label={t('auth.usernameOrEmail')} placeholder={t('auth.usernameOrEmail')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} value={username} onChange={event => setUsername(event.currentTarget.value)} disabled={busy} required />
        <AuthPasswordField containerClassName={signInStyles.password} label={t('auth.password')} placeholder={t('auth.password')} name="password" autoComplete="current-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required />
        <div className={signInStyles.submit}><ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || !username.trim() || !password}>{retrySeconds ? retryLabel : t('action.continue')}</Button></ServicesActions></div>
      </form>
      <AuthLinks>{state.options.recovery && <button type="button" disabled={busy} onClick={() => flow.go('forgot')}>{t('auth.forgotPassword')}</button>}{state.options.registration && <button type="button" disabled={busy} onClick={() => flow.go('create')}>{t('auth.createAccount')}</button>}</AuthLinks>
    </>}
    {mode === 'create' && state.options?.registration && <>
      <form className={styles.form} onSubmit={submit(() => { setSubmittedUsername(username.trim().toLowerCase()); return flow.create({username, password, displayName: name, contactAddress: email}); })}>
        <TextField variant="auth" label={t('auth.email')} name="email" type="email" autoComplete="email" value={email} onChange={event => setEmail(event.currentTarget.value)} description={t('services.signup.emailHelp')} required disabled={busy} />
        <ServicesPair>
          <TextField variant="auth" label={t('auth.username')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} value={username} onChange={event => setUsername(event.currentTarget.value)} minLength={3} maxLength={32} required disabled={busy} error={nameTaken ? t('web.signIn.error.accountTaken') : undefined} />
          <TextField variant="auth" label={<ServicesLabel detail={t('field.optional')}>{t('auth.yourName')}</ServicesLabel>} name="name" autoComplete="name" placeholder={t('services.signup.sameUsername')} value={name} onChange={event => setName(event.currentTarget.value)} maxLength={80} disabled={busy} />
        </ServicesPair>
        <ServicesHint>{t('auth.usernameHelp')}</ServicesHint>
        <div><AuthPasswordField label={<ServicesLabel detail={t('auth.passwordRule')}>{t('auth.password')}</ServicesLabel>} name="new-password" autoComplete="new-password" value={password} onChange={event => setPassword(event.currentTarget.value)} disabled={busy} required aria-describedby={password ? 'signup-strength' : undefined} /><ServicesStrength password={password} id="signup-strength" /></div>
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || !/^\S+@\S+\.\S+$/.test(email.trim()) || username.trim().length < 3 || !passwordStrength(password).acceptable}>{retrySeconds ? retryLabel : t('action.continue')}</Button></ServicesActions>
      </form>
      <AuthLinks>{backAction}</AuthLinks><ServicesTerms />
    </>}
    {mode === 'verify' && state.registration && <>
      <form className={styles.form} onSubmit={submit(() => {if (flow.getSnapshot() !== state) return Promise.resolve(); if (chooseRegistrationName) setSubmittedUsername(username.trim().toLowerCase()); return flow.verify(chooseRegistrationName ? '' : code, chooseRegistrationName ? username : undefined, chooseRegistrationName ? name : undefined);})}>
        {chooseRegistrationName ? <><TextField variant="auth" label={t('auth.username')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} value={username} onChange={event => setUsername(event.currentTarget.value)} minLength={3} maxLength={32} required disabled={busy} error={nameTaken ? t('web.signIn.error.accountTaken') : undefined} /><TextField variant="auth" label={<ServicesLabel detail={t('field.optional')}>{t('auth.yourName')}</ServicesLabel>} name="name" autoComplete="name" value={name} onChange={event => setName(event.currentTarget.value)} maxLength={80} disabled={busy} /><ServicesHint>{t('auth.usernameHelp')}</ServicesHint></> : <ServicesVerificationCode><AuthCodeBoxes length={6} value={code} onChange={setCode} onComplete={() => {}} label={t('auth.code')} disabled={busy} error={codeRejected ? t('services.signup.codeInvalid') : undefined} /></ServicesVerificationCode>}
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || (chooseRegistrationName ? username.trim().length < 3 : code.length !== 6)}>{retrySeconds ? retryLabel : t('auth.createAccount')}</Button></ServicesActions>
      </form>
      {!chooseRegistrationName && <AuthLinks><button type="button" disabled={blocked || state.resends >= 5} onClick={() => void flow.resend()}>{t('auth.sendCodeAgain')}</button><ServicesQuiet>{t('services.signup.resendLimit')}</ServicesQuiet></AuthLinks>}
      <AuthLinks>{!chooseRegistrationName && <button type="button" disabled={busy} onClick={() => flow.startAgain()}>{t('services.signup.startAgain')}</button>}{back}</AuthLinks>{!chooseRegistrationName && <ServicesQuiet>{t('services.signup.codeHelp')}</ServicesQuiet>}
    </>}
    {mode === 'forgot' && state.options?.recovery && <>
      <form className={styles.form} onSubmit={submit(() => flow.getSnapshot() === state ? flow.recover(username) : Promise.resolve())}>
        <TextField variant="auth" name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} label={t('auth.usernameOrEmail')} placeholder={t('auth.usernameOrEmail')} value={username} onChange={event => setUsername(event.currentTarget.value)} maxLength={320} required disabled={busy} />
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || !username.trim()}>{retrySeconds ? retryLabel : t('auth.resetLinkAction')}</Button></ServicesActions>
      </form><ServicesQuiet>{t('auth.privacySameAnswer')}</ServicesQuiet><ServicesQuiet>{t('services.recovery.provider')}</ServicesQuiet><AuthLinks>{back}</AuthLinks>
    </>}
    {mode === 'sent' && <><ServicesQuiet>{t('services.recovery.expiry')}</ServicesQuiet><AuthLinks>{back}</AuthLinks></>}
    {(mode === 'factor' || mode === 'recovery') && <>
      <form className={styles.form} onSubmit={submit(() => flow.signIn(username, password, code))}>
        {mode === 'factor' ? <ServicesVerificationCode label={false}><AuthCodeBoxes length={6} value={code} onChange={setCode} onComplete={() => {}} label={t('auth.code')} disabled={busy} /></ServicesVerificationCode> : <TextField variant="auth" name="recovery-code" autoComplete="one-time-code" label={t('accounts.signIn.recoveryCode')} placeholder={t('services.recovery.codePlaceholder')} value={code} onChange={event => setCode(event.currentTarget.value)} disabled={busy} />}
        {mode === 'factor' && <AuthLinks><span>{t('services.signin.lostPhone')}</span><button type="button" disabled={busy} onClick={() => flow.recovery()}>{t('services.signin.recovery')}</button></AuthLinks>}
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || (mode === 'factor' ? code.length !== 6 : !code.trim())}>{retrySeconds ? retryLabel : t('action.continue')}</Button></ServicesActions>
      </form><AuthLinks>{mode === 'recovery' ? <button type="button" disabled={busy} onClick={() => flow.recovery()}>{t('services.signin.authenticator')}</button> : back}</AuthLinks>
    </>}
    {mode === 'provider' && <>
      <form className={styles.form} onSubmit={submit(() => {setSubmittedUsername(username.trim().toLowerCase()); return flow.finishProvider(username, name);})}>
        <TextField variant="auth" label={t('auth.username')} name="username" autoComplete="username" autoCapitalize="off" spellCheck={false} description={t('services.provider.usernameHelp')} error={nameTaken ? t('web.signIn.error.accountTaken') : undefined} value={username} onChange={event => setUsername(event.currentTarget.value)} minLength={3} maxLength={32} required disabled={busy} />
        <TextField variant="auth" label={<ServicesLabel detail={t('field.optional')}>{t('auth.yourName')}</ServicesLabel>} name="name" autoComplete="name" value={name} onChange={event => setName(event.currentTarget.value)} disabled={busy} />
        <ServicesActions><Button variant="primary" type="submit" loading={busy} disabled={blocked || username.trim().length < 3}>{retrySeconds ? retryLabel : t('action.continue')}</Button></ServicesActions>
      </form><AuthLinks><button type="button" disabled={busy} onClick={() => void flow.cancelProvider()}><ServicesBackAction>{t('action.cancel')}</ServicesBackAction></button></AuthLinks><ServicesTerms provider={state.provider} />
    </>}
    {mode === 'ready' && <>{readyAccount && state.provider && <ServicesProviderAccount {...readyAccount} provider={state.provider} />}
      <ServicesActions><Button variant="primary" loading={busy} disabled={!readyAccount} onClick={() => void flow.continue()}>{t('action.continue')}</Button></ServicesActions><ServicesProviderReturnHint /></>}
    {mode === 'signin' && after}
  </>;
  return <DocumentBootReady>{invitation && mode === 'signin' ? <ServicesInvitationCredentials>{content}</ServicesInvitationCredentials> : embedded ? content : <ServicesAuthStage legal={mode !== 'create' && mode !== 'provider' && mode !== 'verify'} variant={mode === 'provider' ? 'providerOnboarding' : mode === 'create' ? 'signup' : mode === 'verify' ? 'reset' : mode === 'ready' ? 'state' : 'auth'}>{content}</ServicesAuthStage>}</DocumentBootReady>;
}
