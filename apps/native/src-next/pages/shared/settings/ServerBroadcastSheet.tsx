import React from 'react';
import {Button, FormError, TextField} from '../../../kit/controls';
import {SectionCard} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {useServerBroadcast} from './useServerBroadcast';

export function ServerBroadcastSheet(props: Parameters<typeof useServerBroadcast>[0]) {
  const {t, current, message, audience, busy, problem, length, tooLong, close, send, edit} = useServerBroadcast(props);
  return <BottomSheet open={props.open && current} onOpenChange={open => {if (!open) close();}} testID="server-message-everyone"
    title={t('server.messageEveryone')} keyboardAvoiding fitContent dismissOnBack={!busy} dismissOnGesture={!busy}
    dismissOnOutsidePress={!busy} showClose={!busy} footer={<>
      <Button variant="glass" disabled={busy} onPress={close}>{t('action.cancel')}</Button>
      <Button loading={busy} disabled={busy || !length || tooLong} onPress={send}>{t('feedback.send')}</Button>
    </>}>
    <SectionCard>
      <TextField label={t('web.broadcast.message')} value={message} onChangeText={message => edit({message})} editable={!busy} multiline/>
      <OverlayFootRow layout="halves">
        <Button size="compact" disabled={busy} variant={audience === 'everyone' ? 'primary' : 'glass'}
          onPress={() => edit({audience: 'everyone'})}>{t('web.together.everyone')}</Button>
        <Button size="compact" disabled={busy} variant={audience === 'admins' ? 'primary' : 'glass'}
          onPress={() => edit({audience: 'admins'})}>{t('web.broadcast.adminOnly')}</Button>
      </OverlayFootRow>
      {tooLong && <FormError placement="inline">{t('next.settings.general.messageTooLong', {count: length})}</FormError>}
      {problem && <FormError placement="inline">{problem}</FormError>}
    </SectionCard>
  </BottomSheet>;
}
