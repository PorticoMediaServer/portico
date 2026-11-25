import React from 'react';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {AccountEntryCode, AccountEntryCodeNote} from '../../../../kit/accounts/AccountEntryParts';
import {TVCodePresenter} from '../../../../tv/auth';
import type {EntryStageContext} from '../../../../pages/shared/accounts/entry/EntryPrimitives';

const t = defaultI18n.t;

/** Renders the TV activation code stage (no-code-now, TV presenter, and handheld "Use a code from another device"). */
export function renderEntryTVCodeStage({view, kit}: EntryStageContext): React.ReactElement {
  const {
    setStage, busy, error, setError, server, tvState, tvKind,
    busyRef, stageRef, back, tvDoor, noCodeNow, renderedTV, codeBack, tvDevice,
  } = view;
  const {status, layout, tvReason} = kit;

  const consentBody = tvKind === 'local' && server
    ? tvState.verificationUri ? t('accounts.consent.local', {server: server.name, address: tvState.verificationUri}) : undefined
    : t('accounts.consent.portico');
  const approval = renderedTV?.approval();
  const retryApproval = approval?.phase === 'failed' ? () => {void renderedTV?.retryApproval();} : undefined;
  const newCode = (approval ? approval.phase === 'terminal' && renderedTV?.canTypeCredentials()
    : tvState.phase === 'error' || tvState.phase === 'denied' || error)
    ? () => {void renderedTV?.newCode();} : undefined;

  // A Portico code that could not be created keeps the Activate composition: the card says why and the screen asks again
  // every 30 seconds through newCode, which refuses a second request while one is in flight.
  if (noCodeNow) {
    return <TVCodePresenter mode="portico" body={consentBody} busy={busy} reason={tvReason() ?? t('activation.unreachable')}
      onTypeCredentials={() => {void renderedTV?.credentials();}} onBack={codeBack} credentialsDisabled={!renderedTV?.canTypeCredentials()}
      onLocal={tvDoor ? () => {if (renderedTV?.current() && !busyRef.current) {back(); setError(undefined); stageRef.current = 'address'; setStage('address');}} : undefined}/>;
  }

  if (tvDevice) {
    return <TVCodePresenter {...(tvKind === 'local' ? {mode: 'local' as const, serverName: server!.name} : {mode: 'portico' as const})}
      body={consentBody}
      verificationUri={tvState.verificationUri} verificationUriComplete={tvState.verificationUriComplete} userCode={tvState.userCode}
      status={<>{status}{(tvState.phase === 'denied' || tvState.phase === 'error') && <AccountEntryCodeNote>{t(tvState.phase === 'denied' ? 'activation.declined' : 'activation.failed')}</AccountEntryCodeNote>}</>}
      busy={busy} onTypeCredentials={() => {void renderedTV?.credentials();}} onBack={codeBack}
      onLocal={tvDoor ? () => {if (renderedTV?.current() && !busyRef.current) {back(); setError(undefined); stageRef.current = 'address'; setStage('address');}} : undefined}
      onNewCode={newCode} onRetry={retryApproval} retryAt={approval?.retryAt}
      credentialsDisabled={!!approval && !renderedTV?.canTypeCredentials()}/>;
  }

  // A phone or tablet signing in with a code another device approves: its own title ("Use a code from another device", the
  // button that opened it), not the TV's "Activate Portico on this TV".
  return layout(
    t('auth.useCodeFromAnotherDevice'),
    consentBody,
    <AccountEntryCode qr={tvState.verificationUriComplete} code={tvState.userCode ?? t('activation.preparingCode')} address={tvState.verificationUri}
      note={t(tvState.phase === 'denied' ? 'activation.declined' : tvState.phase === 'error' ? 'activation.failed' : 'activation.codeRefreshes')}/>,
    retryApproval
      ? {id: 'retry-approval', label: t('action.tryAgain'), disabled: busy || (approval?.retryAt ?? 0) > Date.now(), loading: busy, onPress: retryApproval}
      : newCode
        ? {id: 'new-code', label: t('activation.getNewCode'), disabled: busy, loading: busy, onPress: newCode}
        : undefined,
  );
}
