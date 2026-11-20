import React from 'react';
import {webhookEvents, webhookMaximum} from '../../../../../../packages/client-core/src/api-access';
import {deliveryProgress, deliveryProblem, webhookEventGroups, webhookEventLabel, webhookTrouble} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {IntegrationNotice, IntegrationValue, SettingsCheckRow} from '../../../kit/settings/IntegrationParts';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {type WebhookGroup, useServerWebhooks, useWebhookGroup} from './useServerWebhooks';

export function ServerWebhooks({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = useWebhookGroup({ports, runtime});
  return group ? <WebhooksPanel group={group} runtime={runtime}/> : <SectionCard><StateRow kind="loading"/></SectionCard>;
}

function WebhooksPanel({group, runtime}: {group: WebhookGroup; runtime: NativeShellRuntime}) {
  const hooks = useServerWebhooks({group, runtime}), {t} = runtime.text;
  const {state, writable, heldMenu, heldEditor, heldConfirmation, heldCard, log} = hooks;
  const deliverySelected = !!log && state.selected === log.hook;
  const deliveryError = state.error === 'deliveries';
  const status = hooks.testResult && (hooks.testResult.delivered ? t('api.webhooks.delivery.delivered')
    : deliveryProblem(t, defaultI18n.has, {...hooks.testResult, state: 'gave_up'}));
  return <>
    <SettingsHelp testID="server-integrations-webhooks">{t('api.webhooks.lede')}</SettingsHelp>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button size="compact" disabled={!writable || (state.list?.items.length ?? 0) >= webhookMaximum}
        onPress={() => hooks.openEditor()}>{t('api.webhooks.add')}</Button>
      <Button size="compact" variant="glass" disabled={state.busy} onPress={hooks.refresh}>{t('web.selection.refresh')}</Button>
    </OverlayFootRow></PanelBlock>
    {status && <IntegrationNotice text={status} error={!hooks.testResult?.delivered}/>}
    {(state.error || state.uncertain) && <IntegrationNotice error text={t(state.uncertain ? 'error.unconfirmedChange' : 'error.unavailable')}/>}
    {state.phase === 'loading' && <SectionCard><StateRow kind="loading"/></SectionCard>}
    {state.list?.items.length === 0 && <SectionCard><StateRow kind="empty" icon="i-link" title={t('api.webhooks.none')}/></SectionCard>}
    {state.list?.items.map(hook => {
      const trouble = webhookTrouble(t, hook, defaultI18n.has);
      const toggle = t('api.webhooks.turnOn', {name: hook.name});
      return <SectionCard key={hook.id}>
        <SettingsRow first label={hook.name} description={[hook.url, t('api.webhooks.eventCount', {count: hook.events.length}),
          trouble?.text].filter((part): part is string => !!part)} onPress={() => hooks.openMenu(hook)}/>
        <SettingsRow label={toggle} control={<Switch accessibilityLabel={toggle} checked={hook.enabled}
          disabled={!writable} onCheckedChange={enabled => hooks.enable(hook, enabled)}/>}/>
      </SectionCard>;
    })}
    <Menu open={!!heldMenu} onOpenChange={open => {if (!open) hooks.closeMenu();}} presentation="sheet"
      title={heldMenu?.hook.name} items={hooks.menuItems}/>
    <BottomSheet open={!!heldEditor} onOpenChange={open => {if (!open) hooks.closeEditor();}}
      title={t(heldEditor?.hook ? 'web.selection.edit' : 'api.webhooks.add')} keyboardAvoiding defaultDetent={1}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={hooks.closeEditor}>{t('action.cancel')}</Button>
        <Button disabled={!hooks.canSave} loading={state.busy} onPress={() => {void hooks.save();}}>{t('action.save')}</Button>
      </OverlayFootRow>}>
      <PanelBlock><TextField label={t('builder.name')} value={hooks.name} maxLength={120} editable={!state.busy && !state.uncertain} onChangeText={hooks.changeName}/></PanelBlock>
      <PanelBlock><TextField label={t('api.webhooks.url')} value={hooks.url} maxLength={2048} editable={!state.busy && !state.uncertain}
        autoCapitalize="none" autoCorrect={false} onChangeText={hooks.changeUrl}/></PanelBlock>
      {webhookEventGroups(webhookEvents).map(group => <SectionCard key={group.id} label={t(group.label)}>
        <OverlayFootRow layout="halves">
          <Button variant="text" size="small" disabled={!writable} onPress={() => hooks.toggleGroup(group.events, true)}>{t('api.webhooks.groupAll')}</Button>
          <Button variant="text" size="small" disabled={!writable} onPress={() => hooks.toggleGroup(group.events, false)}>{t('api.webhooks.groupNone')}</Button>
        </OverlayFootRow>
        {group.events.map(event => <SettingsCheckRow key={event} label={webhookEventLabel(t, defaultI18n.has, event)}
          checked={hooks.events.includes(event)} disabled={!writable} onPress={() => hooks.toggleEvent(event)}/>)}
      </SectionCard>)}
    </BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={open => {if (!open) hooks.closeConfirmation();}}
      title={t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotateTitle' : 'api.webhooks.removeTitle', {name: heldConfirmation?.hook.name ?? ''})}
      description={t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotateBody' : 'api.webhooks.removeBody')}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={hooks.closeConfirmation}>{t('action.cancel')}</Button>
        <Button variant={heldConfirmation?.kind === 'remove' ? 'danger' : 'primary'} disabled={!writable} loading={state.busy}
          onPress={() => {void hooks.confirm();}}>{t(heldConfirmation?.kind === 'rotate' ? 'api.webhooks.rotate' : 'web.missingTitles.removeConfirm')}</Button>
      </OverlayFootRow>}>{null}</BottomSheet>
    <BottomSheet open={!!heldCard} dismissOnBack={false} dismissOnGesture={false} dismissOnOutsidePress={false} showClose={false}
      onOpenChange={open => {if (!open) hooks.closeCard();}} title={t('api.webhooks.secretTitle')} description={t('api.webhooks.secretShownOnce')}
      footer={<>
        <OverlayFootRow layout="halves">
          <Button variant="glass" onPress={() => {void hooks.cardIO('copy');}}>{t('web.twoStep.copy')}</Button>
          <Button variant="glass" onPress={() => {void hooks.cardIO('share');}}>{t('api.tokens.share')}</Button>
        </OverlayFootRow>
        <Button align="stretch" onPress={hooks.closeCard}>{t('web.access.copiedIt')}</Button>
      </>}>
      {heldCard?.issued.secret && <IntegrationValue value={heldCard.issued.secret}/>}
      {hooks.copied && <IntegrationNotice text={t('api.credential.copied')}/>}
      {hooks.copyError && <IntegrationNotice error text={t('error.unavailable')}/>}
    </BottomSheet>
    <BottomSheet open={!!log} onOpenChange={open => {if (!open) hooks.closeLog();}} title={t('api.webhooks.log')}
      description={log?.hook.name} defaultDetent={1} footer={<OverlayFootRow layout="halves">
        <Button onPress={hooks.closeLog}>{t('action.done')}</Button>
        <Button variant="glass" onPress={hooks.refreshLog}>{t('web.selection.refresh')}</Button>
      </OverlayFootRow>}>
      {deliverySelected && <SectionCard>
        {deliveryError && <StateRow kind="error" icon="i-alert" title={t('error.unavailable')}
          action={{label: t('action.tryAgain'), onPress: hooks.refreshLog}}/>}
        {!state.deliveries && !deliveryError && <StateRow kind="loading"/>}
        {state.deliveries?.items.length === 0 && !deliveryError && <StateRow kind="empty" icon="i-logs" title={t('api.webhooks.logEmpty')}/>}
        {state.deliveries?.items.map((delivery, index) => <SettingsRow key={index} first={index === 0 && !deliveryError}
          label={delivery.event === 'test' ? t('api.webhooks.testEvent') : webhookEventLabel(t, defaultI18n.has, delivery.event)}
          description={[
            t(delivery.state === 'waiting' ? 'api.webhooks.delivery.pending' : delivery.state === 'gave_up' ? 'api.webhooks.delivery.failed' : 'api.webhooks.delivery.delivered'),
            defaultI18n.dateTime(delivery.createdAt), deliveryProgress(t, delivery, iso => defaultI18n.time(Date.parse(iso))),
            deliveryProblem(t, defaultI18n.has, delivery),
          ].filter((part): part is string => !!part)}/>)}
        {state.deliveries?.nextCursor && <Button align="stretch" size="compact" onPress={hooks.more}>{t('action.showMore')}</Button>}
      </SectionCard>}
    </BottomSheet>
  </>;
}
