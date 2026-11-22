import React from 'react';
import {FormError, TextField} from '../../../kit/controls';
import {BottomSheet, Menu} from '../../../kit/overlays';
import {OwnerDashboardView} from '../../../kit/dashboard/OwnerDashboardView';
import {OwnerSheetFooter} from '../../../kit/dashboard/OwnerParts';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useOwnerDashboard} from '../../../pages/shared/dashboard/useOwnerDashboard';

/** The owner Dashboard on phones and tablets (owner-dashboard.html frames 2–8): the page, a stream's and a job's menu,
 * Stop with a message, and Fix permissions. Every read and action is `useOwnerDashboard`'s. */
export function OwnerDashboardPage(props: {controller: NativeDashboardController; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; inline?: boolean}) {
  const {view, t, busy, streamMenu, jobMenu, stop, fix} = useOwnerDashboard(props);
  return <>
    <OwnerDashboardView {...view}/>
    <Menu open={streamMenu.open} onOpenChange={open => {if (!open) streamMenu.close();}} presentation="sheet" testID="owner-stream-menu"
      title={streamMenu.title} description={streamMenu.description} items={streamMenu.items}/>
    <Menu open={jobMenu.open} onOpenChange={open => {if (!open) jobMenu.close();}} presentation="sheet" testID="owner-job-menu" title={jobMenu.title} description={jobMenu.description} items={jobMenu.items}/>
    <BottomSheet open={stop.open} onOpenChange={open => {if (!open) stop.dismiss();}} testID="owner-stop-sheet" keyboardAvoiding title={stop.title} description={stop.description}
      footer={<OwnerSheetFooter stop cancelLabel={t('action.cancel')} actionLabel={t('web.nowPlaying.stopConfirm')} busy={busy} onCancel={stop.cancel} onAction={stop.confirm}/>}>
      <TextField testID="owner-stop-message" label={t('web.nowPlaying.messageLabel')} description={t('web.nowPlaying.messageHelp')} placeholder={t('web.nowPlaying.messagePlaceholder')}
        value={stop.message} onChangeText={stop.setMessage} maxLength={stop.maxLength} multiline/>
    </BottomSheet>
    <BottomSheet open={fix.open} onOpenChange={open => {if (!open) fix.dismiss();}} testID="owner-fix-permissions-sheet" title={t('next.dashboard.fixPermissions')} description={fix.description}
      footer={<OwnerSheetFooter cancelLabel={t('action.cancel')} actionLabel={t('next.dashboard.fixPermissions')} busy={busy} onCancel={fix.cancel} onAction={fix.confirm}/>}>
      {fix.refusal ? <FormError placement="inline">{fix.refusal}</FormError> : null}
    </BottomSheet>
  </>;
}
