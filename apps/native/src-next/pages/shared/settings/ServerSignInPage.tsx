import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHelp, SettingsManagementCard} from '../../../kit/settings/SettingsParts';
import {SignInMoveProgress} from '../../../kit/settings/SignInMoveProgress';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useServerSignIn} from './useServerSignIn';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerSignInPage({ports, runtime, onOpenPage, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onOpenPage: (page: 'people' | 'remote') => void; retry?: React.ReactNode;
}) {
  const {t} = runtime.text, model = useServerSignIn(ports), {value, error, finishing, finishBusy, finishError} = model;
  const open = (page: 'people' | 'remote') => {if (model.current()) onOpenPage(page);};
  const move = value?.move;
  return <SettingsPageFrame {...frame} title={t('next.settingsSpec.server.signin.title')} testID="server-signin">
    {retry}
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, model.refresh, 'server-console')}/>}
    {!value && error === undefined && <SettingsHelp>{t('next.settings.loading')}</SettingsHelp>}
    {value && <>
      <SectionCard>
        <SettingsRow first label={t('accounts.settings.method')} value={t(value.signIn === 'local' ? 'accounts.settings.local' : 'accounts.settings.portico')}/>
        <SettingsRow label={t('accounts.settings.owner')} value={value.owner.username} description={value.owner.email}/>
      </SectionCard>
      {move && <SectionCard label={t(move.to === 'local' ? 'accounts.settings.movingToLocal' : 'accounts.settings.movingToPortico')}>
        <SettingsRow first testID="server-signin-move"
          label={move.total !== undefined ? t('accounts.settings.waiting', {waiting: move.waiting, total: move.total}) : t('accounts.settings.waitingCount', {count: move.waiting})}
          description={[t(move.to === 'local' ? 'accounts.settings.backInLocal' : 'accounts.settings.backInPortico'),
            t('accounts.settings.movingSince', {date: defaultI18n.date(move.since)})]}/>
        {move.total !== undefined && <SignInMoveProgress total={move.total} waiting={move.waiting}/>}
        <SettingsRow label={t('accounts.settings.openPeople')} navigates onPress={() => open('people')}/>
        {move.waiting > 0 && <SettingsRow label={t('accounts.settings.notMovedTitle')} description={t('accounts.settings.finishNowHelp')}
          control={<Button size="compact" variant="glass" onPress={model.askFinish}>{t('accounts.settings.finishNow')}</Button>}/>}
      </SectionCard>}
      <BottomSheet open={finishing && !!move} onOpenChange={open => {if (!open) model.closeFinish();}} fitContent testID="server-signin-finish"
        dismissOnBack={!finishBusy} dismissOnGesture={!finishBusy} dismissOnOutsidePress={!finishBusy}
        title={t('accounts.settings.finishNow')} description={move ? t('accounts.settings.finishNowConfirm', {count: move.waiting}) : undefined}
        footer={<OverlayFootRow layout="halves">
          <Button variant="glass" disabled={finishBusy} onPress={model.closeFinish}>{t('next.settings.cancel')}</Button>
          <Button loading={finishBusy} disabled={finishBusy} onPress={() => {void model.finish();}}>{t('accounts.settings.finishNow')}</Button>
        </OverlayFootRow>}>
        {finishError !== undefined && <StateView variant="inline" {...runtime.error(finishError, model.finish, 'server-console')}/>}
      </BottomSheet>
      <SectionCard>
        <SettingsRow first label={t('settings.server.people')} navigates onPress={() => open('people')}/>
        <SettingsRow label={t('settings.server.remote')} navigates onPress={() => open('remote')}/>
      </SectionCard>
      {model.isTV && <SettingsManagementCard url={model.url} text={runtime.text} server/>}
      <PanelBlock><OverlayFootRow layout="halves">
        <Button testID="server-signin-open-web" variant="glass" onPress={model.openWeb}>{t('accounts.settings.changeOnWeb')}</Button>
        <Button size="compact" variant="text" icon="i-refresh" onPress={model.refresh}>{t('action.refresh')}</Button>
      </OverlayFootRow></PanelBlock>
    </>}
  </SettingsPageFrame>;
}
