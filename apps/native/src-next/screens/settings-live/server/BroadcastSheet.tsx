import React, {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {sendServerMessage, serverMessageCharacters, serverMessageLength, type ServerMessageAudience} from '../../../../../../packages/client-core/src/server-admin/panel-logic';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {Button, TextField} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {nativeStorageOwner} from './storage/space-source';
type BroadcastDraft = Readonly<{message: string; audience: ServerMessageAudience}>;
type Opening = {
  readonly ports: NativeSettingsLivePorts;
  readonly lifetime: ReturnType<typeof useSettingsRequestScope>['lifetime'];
  readonly retirement?: AbortSignal;
  retired: boolean;
  draft: BroadcastDraft;
  flight?: AbortController;
  problem?: string;
};
const emptyDraft: BroadcastDraft = Object.freeze({message: '', audience: 'everyone'});

/** General and Feedback share one "Message everyone" sheet (definition 12 rule 80): the words go into people's notifications. */
export function NativeBroadcastSheet({ports, runtime, open, onOpenChange}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; open: boolean; onOpenChange: (open: boolean) => void}) {
  const theme = useTheme(), t = runtime.text.t, {api, lifetime} = useSettingsRequestScope(ports.context);
  const active = useRef<Opening | undefined>(undefined), choices = useRef<Pick<BroadcastDraft, 'audience'>>({audience: 'everyone'});
  const [, render] = useState(0), changed = () => render(value => value + 1);
  const allowed = useCallback(() => lifetime.current() && nativeStorageOwner(ports.context) && !runtime.retirementSignal?.aborted, [lifetime, ports.context, runtime.retirementSignal]);
  const retire = (target: Opening) => {
    target.retired = true; target.flight?.abort(); target.flight = undefined; target.draft = emptyDraft; target.problem = undefined;
    if (active.current === target) active.current = undefined;
  };
  // Only a committed opening may act. Replacement and unmount retire the same
  // object that every drawn callback holds; render never publishes authority.
  useLayoutEffect(() => {
    if (!open || !allowed()) return;
    const next: Opening = {ports, lifetime, retirement: runtime.retirementSignal, retired: false, draft: Object.freeze({...emptyDraft, ...choices.current})};
    active.current = next; changed();
    const stop = () => {if (!next.retired) {retire(next); changed();}};
    const off = ports.context.subscribeAuthority(() => {if (!allowed()) stop();});
    runtime.retirementSignal?.addEventListener('abort', stop, {once: true});
    if (!allowed()) stop();
    return () => {off(); runtime.retirementSignal?.removeEventListener('abort', stop); retire(next);};
  }, [open, ports, lifetime, runtime.retirementSignal, allowed]);
  const opening = active.current;
  const held = opening && opening.ports === ports && opening.lifetime === lifetime && opening.retirement === runtime.retirementSignal && !opening.retired ? opening : undefined;
  const draft = held?.draft, {message, audience} = draft ?? emptyDraft, busy = !!held?.flight, problem = held?.problem;
  const current = () => !!held && active.current === held && !held.retired && allowed();
  const editable = () => current() && !!draft && held!.draft === draft && !held!.flight;
  const edit = (patch: Partial<BroadcastDraft>) => {
    if (!editable()) return;
    const next = Object.freeze({...draft!, ...patch}); held!.draft = next; held!.problem = undefined;
    choices.current = {audience: next.audience}; changed();
  };
  const length = serverMessageLength(message), tooLong = length > serverMessageCharacters;
  const close = () => {if (editable()) {retire(held!); changed(); onOpenChange(false);}};
  const send = async () => {
    if (!editable() || !length || tooLong) return;
    const target = held!, original = draft!, request = new AbortController(); target.flight = request; target.problem = undefined; changed();
    const owns = () => current() && target.draft === original && target.flight === request && !request.signal.aborted;
    try {
      if (!owns()) return;
      await sendServerMessage(api, original.audience, original.message, request.signal);
      if (owns()) {retire(target); changed(); onOpenChange(false);}
    } catch (error) {
      if (owns()) {target.problem = presentError(error, 'server-console').body; changed();}
    } finally {
      if (target.flight === request) {target.flight = undefined; if (current()) changed();}
    }
  };
  return <BottomSheet open={open && current()} onOpenChange={next => {if (!next) close();}} testID="server-message-everyone" keyboardAvoiding
    title={t('server.messageEveryone')} dismissOnBack={!busy} dismissOnGesture={!busy} dismissOnOutsidePress={!busy} showClose={!busy}
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={busy} onPress={close}>{t('action.cancel')}</Button>
      <Button loading={busy} disabled={busy || !length || tooLong} onPress={() => {void send();}}>{t('feedback.send')}</Button></View>}>
    <View style={{gap: 12}}>
      <TextField label={t('web.broadcast.message')} value={message} onChangeText={message => edit({message})} editable={!busy} multiline/>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
        <Button size="compact" disabled={busy} variant={audience === 'everyone' ? 'primary' : 'glass'} onPress={() => edit({audience: 'everyone'})}>{t('web.together.everyone')}</Button>
        <Button size="compact" disabled={busy} variant={audience === 'admins' ? 'primary' : 'glass'} onPress={() => edit({audience: 'admins'})}>{t('web.broadcast.adminOnly')}</Button>
      </View>
      {tooLong && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{t('next.settings.general.messageTooLong', {count: length})}</Text>}
      {problem && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{problem}</Text>}
    </View>
  </BottomSheet>;
}
