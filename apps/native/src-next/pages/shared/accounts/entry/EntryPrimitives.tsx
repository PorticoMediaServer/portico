import React from 'react';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {TVAuthLayout} from '../../../../kit/frames/TVAuthLayout';
import {StateLayout} from '../../../../kit/states';
import {FormError, Switch, TextField} from '../../../../kit/controls';
import {PhonePINEntry} from '../../../../kit/accounts/PhonePINEntry';
import {AccountEntryEndedNotice, AccountEntryFields, AccountEntryLegal, AccountEntryPasswordToggle, AccountEntryStep} from '../../../../kit/accounts/AccountEntryParts';
import {AccountForm} from '../../../../kit/accounts/AccountForm';
import {setupElsewhereRefusal} from '../../../../pages/shared/accounts/entry/entry-helpers';
import type {AccountEntryProps} from '../../../../pages/shared/accounts/entry/entry-types';
import type {AccountEntryView} from '../../../../pages/shared/accounts/useAccountEntry';

const t = defaultI18n.t;

export type EntryFieldCredential = 'username' | 'password' | 'new-password' | 'email';

export type EntryKit = ReturnType<typeof createEntryPrimitives>;

export type EntryStageContext = Readonly<{
  view: AccountEntryView;
  props: AccountEntryProps;
  kit: EntryKit;
}>;

/** Builds shared layout, field, status, and legal-footer helpers for all AccountEntryPage stage renderers. */
export function createEntryPrimitives(view: AccountEntryView) {
  const {
    theme, stage, busy, accountEnded, passwordShown, setPasswordShown, acceptTerms, setAcceptTerms,
    server, lifetime, stageRef, busyRef, acting, stageError, backAction, failure, passwordInvalid,
    tvDevice, openLegalLink, providerNotice,
  } = view;

  // Every password field on a touch screen can be shown (auth.html frame 6), the new one and the one you sign in with.
  const revealable = (credential: string | undefined, label: string) => !theme.isTV && label === t('auth.password')
    && (credential === 'new-password' || credential === 'password');

  const field = (
    label: string,
    value: string,
    change: (next: string) => void,
    secure = false,
    credential?: EntryFieldCredential,
    invalid?: string,
    help?: string,
    placeholder?: string,
    oneTime = false,
  ) => <TextField
    key={label}
    label={label}
    value={value}
    onChangeText={change}
    appearance={theme.platform === 'phone' ? 'auth' : undefined}
    secureTextEntry={secure && !(passwordShown && revealable(credential, label))}
    keyboardType={oneTime && !theme.isTV ? 'number-pad' : credential === 'email' ? 'email-address' : undefined}
    inputMode={oneTime && !theme.isTV ? 'numeric' : undefined}
    textContentType={oneTime && !theme.isTV ? 'oneTimeCode' : credential === 'new-password' ? 'newPassword' : credential === 'email' ? 'emailAddress' : credential}
    autoComplete={oneTime && !theme.isTV ? 'one-time-code' : credential === 'password' ? 'current-password' : credential}
    autoCapitalize="none"
    autoCorrect={false}
    editable={!busy}
    error={invalid}
    description={help}
    placeholder={placeholder}
    trailing={revealable(credential, label)
      ? <AccountEntryPasswordToggle shown={passwordShown} label={t(passwordShown ? 'accounts.signIn.hidePassword' : 'accounts.signIn.showPassword')}
          disabled={busy} onPress={() => {if (lifetime.current && stageRef.current === stage && !busyRef.current && !acting.current) setPasswordShown(value => !value);}}/>
      : undefined}/>;

  const codeField = (label: string, value: string, change: (next: string) => void) =>
    field(label, value, change, false, undefined, undefined, undefined, undefined, true);

  // A two-step code on a phone or tablet is six boxes in two threes (auth.html 6, account-10 frame 3, account-31 frame 3);
  // TV keeps its own code field.
  const twoStepField = (value: string, change: (next: string) => void) => theme.isTV
    ? codeField(t('auth.verificationCode'), value, change)
    : <PhonePINEntry length={6} label={t('auth.verificationCode')} value={value} onChange={change} busy={busy} error={Boolean(stageError)}/>;

  const fields = (...children: React.ReactNode[]) => <AccountEntryFields>{children}</AccountEntryFields>;

  // The kit's labelled switch is the box; the legal line below the form links the Terms of Use.
  const ageTerms = <Switch key="age-terms" testID="accounts-age-terms" checked={acceptTerms} disabled={busy} onCheckedChange={setAcceptTerms}>
    {t('services.signup.ageTerms', {terms: t('services.signup.termsOfUse')})}
  </Switch>;

  // A TV that reaches a server nobody has set up says where setup happens.
  const status = tvDevice && setupElsewhereRefusal(stageError)
    ? <FormError placement="block">{t('accounts.setup.elsewhere')}</FormError>
    : failure && !failure.silent
      ? <FormError placement="block">{passwordInvalid ? t('web.signIn.error.credentials', {server: server!.name}) : failure.body}</FormError>
      : accountEnded && stage === 'welcome' ? <AccountEntryEndedNotice>{t('web.signIn.error.accountEnded')}</AccountEntryEndedNotice> : null;

  const providerNoticeView = providerNotice
    ? <FormError placement="block">{t('accounts.provider.unavailable', {provider: t(providerNotice === 'apple' ? 'accounts.provider.apple' : 'accounts.provider.google')})}</FormError>
    : null;

  // Phone and tablet share the compact form column (auth.html frames 4 to 6).
  const FormLayout = theme.isTV ? TVAuthLayout : AccountForm;
  // Pickers and directory/recovery states keep their authored centred composition.
  const EntryLayout = theme.isTV ? TVAuthLayout : ['profiles', 'profile-pin', 'servers', 'account-sign-out', 'tv'].includes(stage) ? StateLayout : FormLayout;
  const formStep = theme.platform === 'phone' && stage.startsWith('setup-')
    ? <AccountEntryStep>{t('accounts.setup.title')}</AccountEntryStep> : undefined;

  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: ReturnType<AccountEntryView['action']>, more?: React.ReactNode) => (
    <EntryLayout testID="accounts-entry" title={title} body={body} kicker={formStep}
      beforeActions={theme.isTV ? <>{before}{status}</> : <>{status}{before}</>}
      primaryAction={primary} secondaryAction={backAction} avoidKeyboard={stage === 'profile-pin'}>
      {more}
    </EntryLayout>
  );

  const legalFooter = <AccountEntryLegal onOpenLink={openLegalLink}/>;

  // Why a TV has no code, in plain words, for the code card (the door and the code screen say the same).
  const tvReason = () => tvDevice && setupElsewhereRefusal(stageError) ? t('accounts.setup.elsewhere')
    : failure && !failure.silent ? failure.category === 'unreachable' || failure.category === 'timeout' ? t('activation.unreachable') : failure.body
    : accountEnded ? t('web.signIn.error.accountEnded') : undefined;

  return {field, codeField, twoStepField, fields, ageTerms, status, providerNoticeView, FormLayout, EntryLayout, formStep, layout, legalFooter, tvReason};
}
