import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {SignInFinishFooter, SignInMoveCard, SignInWebCode} from '../../../kit/settings/SignInParts';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useServerSignIn} from '../../../pages/shared/settings/useServerSignIn';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

/** Server › Sign-in: how people sign in and who owns the server, a sign-in move under way with Finish now, the way to
 * People and Remote access, and the server's web settings for anything else (a code to scan on a TV). */
export function ServerSignInPage({ports, runtime, onOpenPage, retry, ...frame}: Frame & {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime;
  onOpenPage: (page: 'people' | 'remote') => void; retry?: React.ReactNode}) {
  const view = useServerSignIn({ports, onOpenPage}), {t} = runtime.text, value = view.value, move = value?.move;
  return <SettingsPageFrame {...frame} title={t('next.settingsSpec.server.signin.title')} testID="server-signin">
    {retry}
    {view.error !== undefined && <PanelBlock><StateView variant="inline" {...runtime.error(view.error, view.refresh, 'server-console')}/></PanelBlock>}
    {!value && view.error === undefined && <SectionCard><StateRow kind="loading"/></SectionCard>}
    {value && <>
      {/* The settings spec's Method group; as on web (signin-source.ts), the privacy line is the method's help. */}
      <SectionCard label={t('next.settingsSpec.server.signin.groups.0.title')}>
        <SettingsRow first label={t('next.settingsSpec.server.signin.groups.0.rows.0.label')} value={t(value.signIn === 'local' ? 'accounts.settings.local' : 'accounts.settings.portico')}
          description={t(value.signIn === 'local' ? 'accounts.settings.localPrivacy' : 'accounts.settings.porticoPrivacy')}/>
        <SettingsRow label={t('accounts.settings.owner')} value={value.owner.username} description={value.owner.email}/>
      </SectionCard>
      {move && <SectionCard plain><SignInMoveCard title={t(move.to === 'local' ? 'accounts.settings.movingToLocal' : 'accounts.settings.movingToPortico')}
        waiting={move.total !== undefined ? t('accounts.settings.waiting', {waiting: move.waiting, total: move.total}) : t('accounts.settings.waitingCount', {count: move.waiting})}
        backIn={t(move.to === 'local' ? 'accounts.settings.backInLocal' : 'accounts.settings.backInPortico')}
        since={t('accounts.settings.movingSince', {date: defaultI18n.date(move.since)})} progress={view.progress}
        action={<Button size="compact" variant="glass" onPress={() => view.openPage('people')}>{t('accounts.settings.openPeople')}</Button>}>
        {move.waiting > 0 && <SettingsRow label={t('accounts.settings.notMovedTitle')} description={t('accounts.settings.finishNowHelp')}
          control={<Button size="compact" variant="glass" onPress={view.startFinish}>{t('accounts.settings.finishNow')}</Button>}/>}
      </SignInMoveCard></SectionCard>}
      <BottomSheet open={view.finishing} onOpenChange={open => {if (!open) view.closeFinish();}} fitContent testID="server-signin-finish"
        title={t('accounts.settings.finishNow')} description={move ? t('accounts.settings.finishNowConfirm', {count: move.waiting}) : undefined}
        footer={<SignInFinishFooter><Button loading={view.finishBusy} disabled={view.finishBusy} onPress={view.finish}>{t('accounts.settings.finishNow')}</Button>
          <Button variant="glass" disabled={view.finishBusy} onPress={view.cancelFinish}>{t('next.settings.cancel')}</Button></SignInFinishFooter>}>
        {view.finishError !== undefined ? <StateView variant="inline" {...runtime.error(view.finishError, view.finish, 'server-console')}/> : null}
      </BottomSheet>
      <SectionCard>
        <SettingsRow first navigates label={t('settings.server.people')} onPress={() => view.openPage('people')}/>
        <SettingsRow navigates label={t('settings.server.remote')} onPress={() => view.openPage('remote')}/>
      </SectionCard>
      <PanelBlock>
        {view.tv && <SignInWebCode url={view.webURL} label={t('settings.openOnWeb', {url: view.webURL})}/>}
        <SignInFinishFooter>
          <Button testID="server-signin-open-web" variant="glass" onPress={view.openWeb}>{t('accounts.settings.changeOnWeb')}</Button>
          <Button size="compact" variant="text" icon="i-refresh" onPress={view.refresh}>{t('action.refresh')}</Button>
        </SignInFinishFooter>
      </PanelBlock>
    </>}
  </SettingsPageFrame>;
}
