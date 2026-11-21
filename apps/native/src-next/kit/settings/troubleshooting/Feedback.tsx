import React from 'react';
import {feedbackSentFrom, feedbackTriageCategory} from '../../../../../../packages/client-core/src/server-admin/feedback-source';
import type {FeedbackStatus} from '../../../../../../packages/client-core/src/console';
import {feedbackCategoryName, feedbackStatusName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {sentence, when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {ServerBroadcastSheet} from '../../../pages/shared/settings/ServerBroadcastSheet';
import {useFeedbackControls, type Group} from '../../../pages/shared/settings/useTroubleshootingFeedback';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Button, FormError, Select, TextField} from '../../controls';
import {PanelBlock, SectionCard} from '../../frames';
import {BottomSheet} from '../../overlays';
import {SettingsRow} from '../../shell';
import {SettingsHelp} from '../SettingsParts';

const STATUSES: readonly FeedbackStatus[] = ['open', 'in-progress', 'resolved', 'closed'];

export function NativeFeedback({group, ports, runtime}: {group: Group; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const feedback = useFeedbackControls({group});
  const {state, t, shown, loading, busy} = feedback;
  const statusName = (status: FeedbackStatus) => feedbackStatusName(t, defaultI18n.has, status);

  return <>
    <SectionCard>
      <SettingsRow first label={t('server.messageEveryone')} icon="i-mail" navigates onPress={feedback.openMessage}/>
    </SectionCard>
    <SectionCard label={t('server.feedbackTab')}>
      {state.phase === 'error' && <PanelBlock>
        <FormError placement="inline">{t('error.unavailable')}</FormError>
        <Button variant="glass" size="compact" onPress={feedback.reload}>{t('action.tryAgain')}</Button>
      </PanelBlock>}
      {state.phase === 'loading' && <SettingsRow first label={t('next.settings.loading')}/>}
      {state.phase === 'ready' && !state.reports.length && <SettingsRow first label={t('web.feedback.inboxEmpty')} description={t('web.feedback.inboxEmptyBody')}/>}
      {state.reports.map((report, index) => {
        const category = feedbackCategoryName(t, defaultI18n.has, feedbackTriageCategory(report));
        const values = {category, when: when(report.createdAt), about: report.itemId ? 'yes' : 'no'} as const;
        const description = report.deviceName
          ? t('web.feedback.rowSubtitleFrom', {...values, device: report.deviceName})
          : t('web.feedback.rowSubtitle', values);
        return <SettingsRow key={report.id} first={index === 0} label={report.message.split('\n')[0] || category}
          description={description} value={statusName(report.status)} navigates onPress={() => {void feedback.open(report);}}/>;
      })}
      {state.next && <PanelBlock>
        <Button size="compact" variant="glass" disabled={state.phase === 'loading'} onPress={feedback.more}>{t('action.showMore')}</Button>
      </PanelBlock>}
    </SectionCard>
    <ServerBroadcastSheet ports={ports} runtime={runtime} open={feedback.messageOpen} onOpenChange={feedback.setMessageOpen}/>
    <BottomSheet open={!!feedback.held} onOpenChange={open => {if (!open) feedback.close();}}
      title={shown && feedbackCategoryName(t, defaultI18n.has, feedbackTriageCategory(shown.report))}
      description={shown && t('web.feedback.reportedMeta', {when: when(shown.report.createdAt), status: statusName(shown.report.status)})}
      footer={<>
        <Button variant="glass" align="stretch" onPress={feedback.close}>{t('action.cancel')}</Button>
        <Button align="stretch" disabled={!shown || loading || busy} loading={busy} onPress={() => {void feedback.save();}}>{t('web.feedback.updateReport')}</Button>
      </>}>
      <PanelBlock inset={false}>
        {loading && <SettingsRow first label={t('next.settings.loading')}/>}
        {shown && <>
          <SettingsHelp>{shown.report.message}</SettingsHelp>
          {shown.report.deviceName && <SettingsHelp>{t('web.feedback.sentFromDevice', {device: shown.report.deviceName})}</SettingsHelp>}
          {shown.report.client && <SettingsHelp>{t('server.feedback.sentFrom', {...feedbackSentFrom(shown.report)!, platform: sentence(shown.report.client.platform)})}</SettingsHelp>}
          <Select label={t('web.feedback.status')} value={shown.status} disabled={busy}
            options={STATUSES.map(status => ({value: status, label: statusName(status)}))}
            onValueChange={value => {
              const status = STATUSES.find(option => option === value);
              if (status) feedback.change({...shown, status});
            }}/>
          <TextField label={t('web.feedback.replyLabel')} description={t('web.feedback.replyHelp')} value={shown.reply}
            maxLength={2000} editable={!busy} multiline onChangeText={reply => feedback.change({...shown, reply})}/>
        </>}
        {feedback.error && <FormError placement="inline">{t('error.unavailable')}</FormError>}
      </PanelBlock>
    </BottomSheet>
  </>;
}
