import React from 'react';
import {apiScopes} from '../../../../../../packages/client-core/src/api-access';
import {EXPIRY_DAYS, chosenScopes, expiryLabel, scopeTicked} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {IntegrationNotice, IntegrationValue, SettingsCheckRow} from '../../../kit/settings/IntegrationParts';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {type ApiKeyGroup, useServerApiKeys} from './useServerApiKeys';

export function ServerApiKeys({group, runtime, createRequest, consumeCreateRequest}: {
  group: ApiKeyGroup; runtime: NativeShellRuntime; createRequest: number; consumeCreateRequest: (request: number) => boolean;
}) {
  const keys = useServerApiKeys({group, runtime, createRequest, consumeCreateRequest});
  const {state, frozen, heldForm, heldCard, heldConfirmation} = keys, {t} = runtime.text;
  const issueLabel = t(state.pendingCreate ? 'action.tryAgain' : 'web.access.createKey');
  const issueError = state.error ? <IntegrationNotice error text={t(state.pendingCreate ? 'error.unconfirmedChange' : 'error.unavailable')}/> : null;
  const secret = heldCard?.issued.secret;

  return <>
    <SettingsHelp testID="server-integrations-keys">{t('api.tokens.lede')}</SettingsHelp>
    <PanelBlock><Button size="compact" icon="i-plus" disabled={state.busy || state.phase === 'loading'} onPress={keys.openForm}>{issueLabel}</Button></PanelBlock>
    {issueError}
    {state.phase === 'error' && !state.pendingCreate && <PanelBlock>
      <Button size="compact" variant="glass" onPress={keys.retry}>{t('action.tryAgain')}</Button>
    </PanelBlock>}
    <SectionCard>
      {state.phase === 'loading' && <StateRow kind="loading"/>}
      {state.page?.items.length === 0 && <StateRow kind="empty" icon="i-key" title={t('web.access.noApiKeys')}/>}
      {state.page?.items.map((key, index) => <SettingsRow key={key.id} first={index === 0} label={key.name}
        onPress={state.busy ? undefined : () => keys.openMenu(key)}
        description={[
          key.scopes.map(scope => t(`api.scope.${scope}` as MessageId)).join(', '),
          t(`api.credential.state.${key.state}` as MessageId),
          key.expiresAt ? t('api.credential.expires', {when: defaultI18n.date(key.expiresAt)}) : t('api.credential.noExpiry'),
          key.lastUsedAt ? t('api.credential.lastUsed', {when: defaultI18n.date(key.lastUsedAt)}) : t('api.credential.neverUsed'),
        ].filter(Boolean)}/>)}
    </SectionCard>
    {(keys.hasPrevious || !!state.page?.nextCursor) && <PanelBlock><OverlayFootRow layout="halves">
      {keys.hasPrevious && <Button size="compact" disabled={frozen} onPress={keys.previousPage}>{t('action.back')}</Button>}
      {!!state.page?.nextCursor && <Button size="compact" disabled={frozen} onPress={keys.nextPage}>{t('action.showMore')}</Button>}
    </OverlayFootRow></PanelBlock>}
    <Menu open={!!keys.menu} onOpenChange={open => {if (!open) keys.closeMenu();}} presentation="sheet" title={keys.menu?.key.name} items={keys.menuItems}/>
    <BottomSheet open={!!heldForm} onOpenChange={open => {if (!open) keys.closeForm();}} title={t('web.access.createKeyTitle')}
      keyboardAvoiding defaultDetent={1} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={keys.closeForm}>{t('action.cancel')}</Button>
        <Button disabled={state.busy || !state.pendingCreate && (!keys.name.trim() || !chosenScopes(keys.ticked).length)}
          loading={state.busy} onPress={() => {void keys.issue();}}>{issueLabel}</Button>
      </OverlayFootRow>}>
      {state.pendingCreate ? <SettingsHelp>{t('error.unconfirmedChange')}</SettingsHelp> : <>
        <PanelBlock><TextField label={t('builder.name')} description={t('web.access.keyNameHelp')} value={keys.name}
          maxLength={80} editable={!frozen} onChangeText={keys.changeName}/></PanelBlock>
        {apiScopes.map(scope => <SettingsCheckRow key={scope} label={t(`api.scope.${scope}` as MessageId)}
          description={t(`api.scope.${scope}.help` as MessageId)} checked={scopeTicked(keys.ticked, scope)}
          disabled={frozen} onPress={() => keys.changeScope(scope)}/>)}
        <PanelBlock><Select label={t('api.credential.expiryLabel')} value={String(keys.days)} disabled={frozen}
          options={EXPIRY_DAYS.map(value => ({value: String(value), label: expiryLabel(t, value)}))} onValueChange={keys.changeDays}/></PanelBlock>
      </>}
      {issueError}
    </BottomSheet>
    <BottomSheet open={!!heldCard} dismissOnBack={false} dismissOnGesture={false} dismissOnOutsidePress={false} showClose={false}
      onOpenChange={open => {if (!open) keys.closeCard();}} title={t('web.access.copyKeyTitle')} description={t('next.ownerKeys.once')}
      footer={<>
        {secret && <OverlayFootRow layout="halves">
          <Button size="compact" variant="glass" icon="i-copy" onPress={() => {void keys.cardIO('copy');}}>{t('web.twoStep.copy')}</Button>
          <Button size="compact" variant="glass" onPress={() => {void keys.cardIO('share');}}>{t('api.tokens.share')}</Button>
        </OverlayFootRow>}
        <Button align="stretch" onPress={keys.closeCard}>{t('action.done')}</Button>
      </>}>
      {secret ? <IntegrationValue value={secret}/> : <SettingsHelp>{t('integrations.credential.retryReveal')}</SettingsHelp>}
      {keys.copied && <IntegrationNotice text={t('api.credential.copied')}/>}
      {keys.cardError && <IntegrationNotice error text={t('error.unavailable')}/>}
    </BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={open => {if (!open) keys.closeConfirmation();}}
      title={heldConfirmation ? t('web.access.revokeTitle', {name: heldConfirmation.key.name}) : undefined} description={t('web.access.revokeBody')}
      dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" disabled={state.busy} onPress={keys.closeConfirmation}>{t('action.cancel')}</Button>
        <Button variant="danger" disabled={state.busy} loading={state.busy} onPress={() => {void keys.revoke();}}>{t('web.access.revoke')}</Button>
      </OverlayFootRow>}>{null}</BottomSheet>
  </>;
}
