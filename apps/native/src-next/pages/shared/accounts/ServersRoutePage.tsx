import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {TVAuthLayout} from '../../../kit/frames/TVAuthLayout';
import {StateLayout} from '../../../kit/states';
import {Button, TextField, FormError} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {ServerRemoveDownloads, ServerSignOutBody, ServerSignOutFooter, ServerSignOutText, ServersConsentChoices, ServersConsentCodeBox, ServersConsentReview,
  ServersFooter} from '../../../kit/accounts/ServersRouteParts';
import {AccountEntry} from '../../../screens/accounts/AccountEntry';
import {ServerChooserPage} from '../../touch/accounts/ServerChooserPage';
import {TVServerChooserPage, TVServerSettingsPanel} from '../../tv/accounts/ServerChooserPage';
import {TrustedCertificatesPage} from './TrustedCertificatesPage';
// Named through pages/shared (not `./`) so the screen harness loads the hook for real when a test loads this page.
import {useServersRoute, type ServersRouteProps} from '../../../pages/shared/accounts/useServersRoute';

const t = defaultI18n.t;

/** This app's servers: the chooser (phone, tablet and TV, or inside a TV's Settings), Use a local server, Link a TV, Sign out of
 * Portico Account, and Sign out or Forget a server with its downloads choice. Every action and its guard is `useServersRoute`'s.
 * Also usable when no selected server can restore: retained servers remain reachable. */
export function ServersRoutePage(props: ServersRouteProps) {
  const view = useServersRoute(props), {panel, busy} = view;
  const errorView = view.errorBody !== undefined ? <FormError placement="block">{view.errorBody}</FormError> : null;
  const AccountLayout = view.tv ? TVAuthLayout : StateLayout;
  const layout = (title: string, body?: string, before?: React.ReactNode, primary?: typeof view.doneAction, more?: React.ReactNode) => <AccountLayout testID="accounts-servers" title={title} body={body}
    beforeActions={<>{before}{errorView}</>} primaryAction={primary} secondaryAction={view.secondaryAction}>{more}</AccountLayout>;
  if (panel === 'add') return <AccountEntry key={view.addGeneration} downloads={view.addDownloads} onSetupComplete={props.onSetupComplete} initialStage="address"
    onBack={view.addBack} onComplete={view.addComplete}/>;
  if (panel === 'sign-out-account') return layout(t('accounts.signOut.portico'), undefined, undefined, view.signOutAccountAction);
  if (panel === 'consent-kind') return layout(t('settings.linkTV'), t(view.consentUnavailable ? 'linkTV.signInFirst' : 'linkTV.enterCode'), <ServersConsentChoices>
    {view.localConsentOffered && <Button variant="glass" disabled={busy} onPress={view.chooseLocalConsent}>{t('accounts.session.local')}</Button>}
    {/* Only while this app holds a Portico Account: without one the choice could only fail. */}
    {view.accountHeld && <Button variant="glass" disabled={busy} onPress={view.choosePorticoConsent}>{t('account.portico')}</Button>}
  </ServersConsentChoices>);
  // The phone's side of Link a TV: what approving does (the TV's own instructions, accounts.consent.*, belong on the TV).
  if (panel === 'consent-code') return layout(t('settings.linkTV'), t('linkTV.approveCaption', {where: t(view.consentPortico ? 'linkTV.scopeAccount' : 'linkTV.scopeServer')}),
    <ServersConsentCodeBox><TextField label={t('linkTV.enterCode')} value={view.code} onChangeText={view.changeCode}
      autoCapitalize="characters" maxLength={9} autoCorrect={false} editable={!busy}/></ServersConsentCodeBox>,
    view.reviewAction);
  if (panel === 'consent-review') return layout(view.reviewTitle, t('web.device.approveWarn'), <ServersConsentReview facts={view.previewFacts}/>,
    view.approveAction, <Button variant="text" disabled={busy} onPress={view.deny}>{t('web.device.notMine')}</Button>);
  if (panel === 'consent-done') return layout(view.done, undefined, undefined, view.doneAction);
  const sheet = view.sheet;
  const signOutSheet = sheet ? <BottomSheet open={sheet.open} onOpenChange={open => {if (!open) sheet.dismiss();}} testID="accounts-sign-out-sheet" title={sheet.title}
    footer={<ServerSignOutFooter label={sheet.confirmLabel} busy={busy} onConfirm={sheet.confirm} cancelLabel={t('action.cancel')} onCancel={sheet.dismiss}/>}>
    <ServerSignOutBody>
      <ServerSignOutText forgetBody={sheet.forgetBody} body={t('settings.signOutConfirm.bodyDownloads')}/>
      {sheet.selectedServer && <ServerRemoveDownloads checked={sheet.removeDownloads} label={t('settings.signOutConfirm.removeDownloads')} onToggle={sheet.toggleRemove}/>}
    </ServerSignOutBody>
  </BottomSheet> : null;
  if (!view.tv) return <><ServerChooserPage certificates={<TrustedCertificatesPage context={props.context}/>} snapshot={view.servers} busy={busy} error={errorView} onBack={view.back}
    onSelect={view.select} onSignOut={view.signOut} onForget={view.forget} onRefresh={view.refresh} onAdd={view.add}
    footer={<ServersFooter>
      <Button variant="text" disabled={busy} onPress={view.linkTV}>{t('settings.linkTV')}</Button>
      {view.accountHeld && <Button variant="text" disabled={busy} onPress={view.signOutPortico}>{t('accounts.signOut.portico')}</Button>}
    </ServersFooter>}/>{signOutSheet}</>;
  const tvProps: React.ComponentProps<typeof TVServerChooserPage> = {snapshot: view.servers, busy, error: errorView, onBack: view.back, owner: view.owner,
    onSelect: view.select, onSignOut: view.signOut, onForget: view.forget, onRefresh: view.refresh, onAdd: view.tvAdd,
    certificates: (bind, refreshServers) => <TrustedCertificatesPage context={props.context} refreshRef={bind('certificates-refresh')}
      refreshDown={refreshServers}/>,
    footer: bind => <ServersFooter>
      <Button ref={bind('link-tv')} focusId="link-tv" variant="link" disabled={busy} onPress={view.tvLinkTV}>
        {t('settings.linkTV')}
      </Button>
      {view.accountHeld && <Button focusId="sign-out-portico" variant="link" disabled={busy} onPress={view.tvSignOutPortico}>
        {t('accounts.signOut.portico')}
      </Button>}
    </ServersFooter>};
  return <>{props.embedded ? <TVServerSettingsPanel {...tvProps}/> : <TVServerChooserPage {...tvProps}/>}{signOutSheet}</>;
}
