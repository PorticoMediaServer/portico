import React, {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Platform,Share, Text, View} from 'react-native';
import {SettingsApiKeysSource} from '../../../../../../../packages/client-core/src/server-admin/api-keys-source';
import {EXPIRY_DAYS, chosenScopes, credentialWorks, expiryInstant, expiryLabel, scopeTicked, type ExpiryDays} from '../../../../../../../packages/client-core/src/server-admin/api-words';
import type {ApiCredential, ApiScope, IssuedApiCredential} from '../../../../../../../packages/client-core/src/api-access';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {copyText} from '../../../../platform/clipboard';
import {Button, Select, TextField} from '../../../../kit/controls';
import {BottomSheet, Menu, type MenuEntry} from '../../../../kit/overlays';
import {shellType, alpha} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';

export function createNativeApiKeys(context: NativeShellBootstrapContext, operationId: () => string, reportError: (error: unknown) => void) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => !Platform.isTV && lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsApiKeysSource(api, current, error => {lifetime.dispose();reportError(error);}, operationId);
  return {context, source, current, dispose: () => {source.dispose();lifetime.dispose();}};
}
type Group = ReturnType<typeof createNativeApiKeys>;
type Form = Readonly<{source: SettingsApiKeysSource; token: object}>;
type Card = Readonly<{source: SettingsApiKeysSource; issued: IssuedApiCredential}>;
type Selection = Readonly<{source: SettingsApiKeysSource; key: ApiCredential}>;

/** Secrets remain in this exact issued card, never in the shared source or a
 * reusable snapshot. Closing a card/form synchronously retires its handlers. */
export function NativeApiKeys({group, runtime, createRequest = 0, consumeCreateRequest}: Readonly<{group: Group; runtime: NativeShellRuntime; createRequest?: number; consumeCreateRequest: (request: number) => boolean}>) {
  const {source} = group, theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [form, setForm] = useState<Form>(), [card, setCard] = useState<Card>(), [menu, setMenu] = useState<Selection>(), [confirmation, setConfirmation] = useState<Selection>();
  const [name, setName] = useState(''), [ticked, setTicked] = useState<ReadonlySet<ApiScope>>(new Set()), [days, setDays] = useState<ExpiryDays>(0), [copied, setCopied] = useState(false), [cardError, setCardError] = useState(false);
  const [cursors, setCursors] = useState<readonly string[]>(['']);
  const view = useMemo(() => ({source, active: true, form: undefined as Form | undefined, card: undefined as Card | undefined, menu: undefined as Selection | undefined, confirmation: undefined as Selection | undefined, io: undefined as object | undefined, request: 0}), [source]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.form = undefined;view.card = undefined;view.menu = undefined;view.confirmation = undefined;view.io = undefined;};}, [view]);
  useEffect(() => {void source.load();}, [source]);
  const current = () => !Platform.isTV && view.active && view.source === source && group.current() && source.getSnapshot().phase !== 'idle';
  const heldForm = form && view.form === form && current() ? form : undefined;
  const heldCard = card && view.card === card && current() ? card : undefined;
  const heldConfirmation = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const editing = () => !!heldForm && current() && view.form === heldForm && !source.getSnapshot().busy && !source.getSnapshot().pendingCreate;
  const openForm = () => {
    if (!current() || source.getSnapshot().busy || view.form) return;
    view.card = undefined;view.io = undefined;setCard(undefined);setCopied(false);setCardError(false);
    const next = {source, token: {}};view.form = next;setForm(next);setName('');setTicked(new Set());setDays(0);
  };
  useEffect(() => {
    if (!createRequest || createRequest === view.request || !view.active || !group.current() || state.phase !== 'ready' && !state.pendingCreate || state.busy) return;
    if (!consumeCreateRequest(createRequest)) return;
    view.request = createRequest;
    const next = {source, token: {}};view.card = undefined;view.io = undefined;view.form = next;
    setCard(undefined);setCopied(false);setCardError(false);setForm(next);setName('');setTicked(new Set());setDays(0);
  }, [createRequest, consumeCreateRequest, view, source, group, state.phase, state.busy, state.pendingCreate]);
  const closeForm = () => {if (!heldForm || !current() || view.form !== heldForm || source.getSnapshot().busy) return;view.form = undefined;setForm(undefined);};
  const closeCard = () => {if (!heldCard || view.card !== heldCard) return;view.card = undefined;view.io = undefined;setCard(undefined);setCopied(false);setCardError(false);};
  const issue = async () => {
    const attempt = heldForm;if (!attempt || !current() || view.form !== attempt || source.getSnapshot().busy) return;
    const out = await (source.getSnapshot().pendingCreate ? source.retryCreate() : source.create({name, scopes: chosenScopes(ticked), ...(days ? {expiresAt: expiryInstant(days, Date.now())} : {})}));
    if (!out || !current() || view.form !== attempt) return;
    view.form = undefined;setForm(undefined);const next = {source, issued: out};view.card = next;setCard(next);setCopied(false);setCardError(false);setCursors(['']);
  };
  const cardIO = async (kind: 'copy' | 'share') => {
    const captured = heldCard;if (!captured?.issued.secret || !current() || view.card !== captured || view.io) return;
    const io = {};view.io = io;
    try {
      if (!current() || view.card !== captured || view.io !== io) return;
      if (kind === 'copy') await copyText(captured.issued.secret);else await Share.share({title: captured.issued.credential.name, message: captured.issued.secret});
      if (current() && view.card === captured && view.io === io) {setCopied(kind === 'copy');setCardError(false);}
    } catch {if (current() && view.card === captured && view.io === io) setCardError(true);}
    finally {if (view.io === io) view.io = undefined;}
  };
  const reveal=async(selection:Selection)=>{
    if(!current()||view.io||!source.getSnapshot().page?.items.includes(selection.key))return;
    const io={};view.io=io;view.menu=undefined;setMenu(undefined);
    const secret=await source.reveal(selection.key);
    if(secret&&current()&&view.io===io){const next={source,issued:{credential:selection.key,secret}};view.card=next;setCard(next);setCopied(false);setCardError(false);}
    if(view.io===io)view.io=undefined;
  };
  const revoke = async () => {
    const captured = heldConfirmation;if (!captured || !current() || view.confirmation !== captured || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(captured.key)) return;
    await source.revoke(captured.key);
    if (current() && view.confirmation === captured) {view.confirmation = undefined;setConfirmation(undefined);}
  };
  const closeConfirmation = () => {if (!current() || view.confirmation !== confirmation || source.getSnapshot().busy) return;view.confirmation = undefined;setConfirmation(undefined);};
  const frozen = state.busy || state.pendingCreate;
  const menuItems: readonly MenuEntry[] = menu && view.menu === menu && current() ? [{id:'reveal',label:t('integrations.credential.reveal'),disabled:state.busy||menu.key.state!=='active',onSelect:()=>{void reveal(menu);}}, {id: 'revoke', label: t('web.access.revoke'), icon: 'i-trash', destructive: true, disabled: state.busy || !credentialWorks(menu.key), onSelect: () => {
    if (!current() || view.menu !== menu || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(menu.key)) return;
    view.menu = undefined;setMenu(undefined);const next = {source, key: menu.key};view.confirmation = next;setConfirmation(next);
  }}] : [];
  return <View testID="server-integrations-keys" style={{marginTop: 14, gap: 12}}>
    <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{t('api.tokens.lede')}</Text>
    <Button size="compact" icon="i-plus" disabled={state.busy || state.phase === 'loading'} onPress={openForm}>{t(state.pendingCreate ? 'action.tryAgain' : 'web.access.createKey')}</Button>
    {state.error && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{t(state.pendingCreate ? 'error.unconfirmedChange' : 'error.unavailable')}</Text>}
    {state.phase === 'loading' && <Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{t('next.settings.loading')}</Text>}
    {state.phase === 'error' && !state.pendingCreate && <Button size="compact" variant="glass" onPress={() => {if (current()) void source.load();}}>{t('action.tryAgain')}</Button>}
    {state.page?.items.length === 0 && <Text style={[shellType(13, 400), {color: theme.colors.textTertiary}]}>{t('web.access.noApiKeys')}</Text>}
    {state.page?.items.map(key => <Button key={key.id} size="compact" variant="glass" disabled={state.busy} onPress={() => {if (!current() || source.getSnapshot().busy || !source.getSnapshot().page?.items.includes(key)) return;const next = {source, key};view.menu = next;setMenu(next);}} style={{alignItems: 'flex-start'}}><View style={{gap: 3}}><Text style={[shellType(15, 700), {color: theme.colors.text}]}>{key.name}</Text><View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[key.scopes.map(scope => t(`api.scope.${scope}` as MessageId)).join(', '), t('api.credential.endsIn', {hint: key.hint})].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12.5, 400, 18), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View><View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[t(`api.credential.state.${key.state}` as MessageId), key.expiresAt ? t('api.credential.expires', {when: defaultI18n.date(key.expiresAt)}) : t('api.credential.noExpiry')].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12, 400, 17), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View><Text style={[shellType(12, 400, 17), {color: theme.colors.textTertiary}]}>{key.lastUsedAt ? t('api.credential.lastUsed', {when: defaultI18n.date(key.lastUsedAt)}) : t('api.credential.neverUsed')}</Text></View></Button>)}
    <View style={{flexDirection: 'row', gap: 8}}>{cursors.length > 1 && <Button size="compact" disabled={frozen} onPress={() => {if (!current() || source.getSnapshot().busy || source.getSnapshot().page !== state.page) return;const next = cursors.slice(0,-1);setCursors(next);void source.load(next[next.length-1]);}}>{t('action.back')}</Button>}{state.page?.nextCursor && <Button size="compact" disabled={frozen} onPress={() => {const cursor = state.page?.nextCursor;if (!current() || source.getSnapshot().busy || source.getSnapshot().page !== state.page || !cursor) return;setCursors([...cursors,cursor]);void source.load(cursor);}}>{t('action.showMore')}</Button>}</View>
    <Menu open={!!menu && view.menu === menu && current()} onOpenChange={open => {if (!open && view.menu === menu) {view.menu = undefined;setMenu(undefined);}}} presentation="sheet" title={menu?.key.name} items={menuItems}/>
    <BottomSheet open={!!heldForm} onOpenChange={open => {if (!open) closeForm();}} title={t('web.access.createKeyTitle')} keyboardAvoiding dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={closeForm}>{t('action.cancel')}</Button><Button disabled={state.busy || !state.pendingCreate && (!name.trim() || !chosenScopes(ticked).length)} loading={state.busy} onPress={() => {void issue();}}>{t(state.pendingCreate ? 'action.tryAgain' : 'web.access.createKey')}</Button></View>}>
      <View style={{gap: 12}}>{state.pendingCreate ? <Text style={[shellType(13, 400), {color: theme.colors.textSecondary}]}>{t('error.unconfirmedChange')}</Text> : <><TextField label={t('builder.name')} description={t('web.access.keyNameHelp')} value={name} maxLength={80} editable={!frozen} onChangeText={value => {if (editing()) setName(value);}}/>{state.options?.scopes.map(scope => <Button key={scope} variant={scopeTicked(ticked, scope) ? 'primary' : 'glass'} size="compact" disabled={frozen} onPress={() => {if (!editing()) return;setTicked(current => {const next = new Set(current);if (next.has(scope)) next.delete(scope);else next.add(scope);return next;});}}><View><Text style={[shellType(14, 600), {color: theme.colors.text}]}>{t(`api.scope.${scope}` as MessageId)}</Text><Text style={[shellType(12, 400, 17), {color: theme.colors.textTertiary}]}>{t(`api.scope.${scope}.help` as MessageId)}</Text></View></Button>)}<Select label={t('api.credential.expiryLabel')} value={String(days)} disabled={frozen} options={EXPIRY_DAYS.map(value => ({value: String(value), label: expiryLabel(t, value)}))} onValueChange={value => {const parsed = Number(value);if (editing() && EXPIRY_DAYS.some(day => day === parsed)) setDays(parsed as ExpiryDays);}}/></>}{state.error && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record}]}>{t(state.pendingCreate ? 'error.unconfirmedChange' : 'error.unavailable')}</Text>}</View>
    </BottomSheet>
    <BottomSheet open={!!heldCard} dismissOnBack={false} dismissOnGesture={false} dismissOnOutsidePress={false} showClose={false} onOpenChange={open => {if (!open) closeCard();}} title={t('web.access.copyKeyTitle')} description={t('integrations.credential.recoverable')} footer={<View style={{gap: 8}}>{heldCard?.issued.secret && <View style={{flexDirection: 'row', gap: 8}}><Button size="compact" variant="glass" icon="i-copy" onPress={() => {void cardIO('copy');}}>{t('web.twoStep.copy')}</Button><Button size="compact" variant="glass" onPress={() => {void cardIO('share');}}>{t('api.tokens.share')}</Button></View>}<Button onPress={closeCard}>{t('action.done')}</Button></View>}>
      {heldCard?.issued.secret ? <View style={{padding: 12, borderRadius: 12, backgroundColor: alpha(theme.colors.ink,.55)}}><Text selectable style={[shellType(12.5, 500, 18), {color: theme.colors.text}]}>{heldCard.issued.secret}</Text></View> : <Text style={[shellType(13, 400), {color: theme.colors.textSecondary}]}>{t('integrations.credential.retryReveal')}</Text>}{copied && <Text style={[shellType(13, 500), {color: theme.colors.accentStrong}]}>{t('api.credential.copied')}</Text>}{cardError && <Text accessibilityRole="alert" style={[shellType(13, 500), {color: theme.colors.record}]}>{t('error.unavailable')}</Text>}
    </BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={open => {if (!open) closeConfirmation();}} title={heldConfirmation ? t('web.access.revokeTitle', {name: heldConfirmation.key.name}) : undefined} description={t('web.access.revokeBody')} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={closeConfirmation}>{t('action.cancel')}</Button><Button variant="glass" disabled={state.busy} loading={state.busy} textStyle={{color: theme.colors.record}} onPress={() => {void revoke();}}>{t('web.access.revoke')}</Button></View>}><View/></BottomSheet>
  </View>;
}
