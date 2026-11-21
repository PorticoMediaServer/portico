import React from 'react';
import {Button} from '../../../kit/controls';
import {PanelBlock} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {InvitationCard, InvitationPictureCapture} from '../../../kit/settings/InvitationCard';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {StateView} from '../../../kit/states';
import type {InvitationOpening, NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useInvitationReady} from './useInvitationReady';

export function InvitationReadySheet({source, opening, runtime, serverName, signIn}: {
  source: NativePeopleSource; opening: InvitationOpening; runtime: NativeShellRuntime; serverName: string; signIn?: 'local' | 'portico';
}) {
  const model = useInvitationReady({source, opening, runtime, serverName}), {t} = runtime.text;
  const {state, link, code} = model;
  return <BottomSheet testID="invitation-ready" open title={t('invite.readyTitle')} defaultDetent={1}
    dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
    onOpenChange={open => {if (!open) model.close();}}
    footer={<Button testID="invitation-ready-done" variant="glass" onPress={model.close}>{t('action.done')}</Button>}>
    <PanelBlock><InvitationCard expiry={model.expiry} serverName={model.serverName} access={model.access}
      link={link} code={code.phase === 'ready' ? code.code : undefined} signIn={signIn} t={t}/></PanelBlock>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button testID="invitation-share" icon="i-share" disabled={!link} onPress={() => {void model.share();}}>{t('invite.share')}</Button>
      <Button testID="invitation-copy-link" variant="glass" icon="i-copy" disabled={!link}
        onPress={() => {void model.copy();}}>{t(model.copied ? 'next.settings.invitations.linkCopied' : 'web.access.copyLink')}</Button>
    </OverlayFootRow></PanelBlock>
    {model.canSavePicture && <PanelBlock><Button testID="invitation-save-picture" variant="glass" icon="i-download"
      loading={model.saving} disabled={!link || model.saving} onPress={() => {void model.save();}}>{t('accounts.invitation.savePicture')}</Button></PanelBlock>}
    <SettingsHelp>{t('accounts.invitation.copyAgainHelp')}</SettingsHelp>
    {code.phase === 'ready' && <SettingsHelp>{t('web.access.shortCodeShownOnce')}</SettingsHelp>}
    {code.phase === 'idle' && <SettingsHelp>{t('web.access.shortCodeHelp')}</SettingsHelp>}
    {code.phase !== 'issuing' && <PanelBlock><Button testID="invitation-new-code" variant="glass" icon="i-plus"
      onPress={model.newCode}>{t('web.access.newShortCode')}</Button></PanelBlock>}
    {code.phase === 'failed' && <StateView variant="inline" {...runtime.error(code.error, model.retryCode, 'server-console')}/>}
    {model.failure !== undefined && <StateView variant="inline" {...runtime.error(model.failure, model.clearFailure, 'server-console')}/>}
    {model.picture && <InvitationPictureCapture picture={model.picture} pictureRef={model.svg}/>}
  </BottomSheet>;
}
