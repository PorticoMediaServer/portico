import {selectWebhookEventGroup, webhookSecretChange} from './webhook-form';
import React, {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Share, Text, View} from 'react-native';
import {SettingsWebhooksSource} from '../../../../../../../packages/client-core/src/server-admin/webhooks-source';
import {webhookEdits, type Webhook, type IssuedWebhook} from '../../../../../../../packages/client-core/src/api-access';
import {deliveryTone, deliveryProgress, deliveryProblem, webhookEventGroups, webhookEventLabel, webhookAddressOk, webhookTrouble} from '../../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {copyText} from '../../../../platform/clipboard';
import {Button, Focusable, Switch, TextField} from '../../../../kit/controls';
import {BottomSheet, Menu, type MenuEntry} from '../../../../kit/overlays';
import {Icon} from '../../../../kit/Icon';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';

function createWebhooks(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T> => current() ? lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsWebhooksSource(api,current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);});
  return {context,source,current,dispose:()=>{source.dispose();lifetime.dispose();}};
}
type Group = ReturnType<typeof createWebhooks>;
type Editor = Readonly<{hook?:Webhook}>;
type Confirmation = Readonly<{hook:Webhook;kind:'remove'|'rotate'}>;
type Card = Readonly<{issued:IssuedWebhook}>;
type ActionMenu = Readonly<{hook:Webhook}>;
export function NativeWebhooks({ports,runtime}:{ports:NativeSettingsLivePorts;runtime:NativeShellRuntime}) {
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<Group>();
  useLayoutEffect(()=>{const next=createWebhooks(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  return group?.context===context ? <Webhooks group={group} runtime={runtime}/> : null;
}
export function Webhooks({group,runtime}:{group:Group;runtime:NativeShellRuntime}) {
  const {source}=group,theme=useTheme(),t=runtime.text.t as typeof defaultI18n.t,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
  const [editor,setEditor]=useState<Editor>(),[confirmation,setConfirmation]=useState<Confirmation>(),[card,setCard]=useState<Card>(),[name,setName]=useState(''),[url,setUrl]=useState(''),[events,setEvents]=useState<readonly string[]>([]),[enabled,setEnabled]=useState(true),[signing,setSigning]=useState(''),[copied,setCopied]=useState(false),[copyError,setCopyError]=useState(false),[log,setLog]=useState<Readonly<{hook:Webhook}>>(),[menu,setMenu]=useState<ActionMenu>(),[replacement,setReplacement]=useState('');
  const view=useMemo(()=>({source,active:true,editor:undefined as Editor|undefined,confirmation:undefined as Confirmation|undefined,card:undefined as Card|undefined,io:undefined as object|undefined,log:undefined as Readonly<{hook:Webhook}>|undefined,menu:undefined as ActionMenu|undefined}),[source]);
  useLayoutEffect(()=>{view.active=true;return()=>{view.active=false;view.editor=undefined;view.confirmation=undefined;view.card=undefined;view.io=undefined;view.log=undefined;view.menu=undefined;};},[view]);
  useEffect(()=>{void source.load();},[source]);
  const current=()=>view.active&&view.source===source&&group.current()&&source.getSnapshot().phase!=='idle';
  const listed=(hook:Webhook)=>!!source.getSnapshot().list?.items.includes(hook);
  const heldEditor=editor&&view.editor===editor&&current()&&(!editor.hook||listed(editor.hook))?editor:undefined;
  const heldConfirmation=confirmation&&view.confirmation===confirmation&&current()&&listed(confirmation.hook)?confirmation:undefined;
  const heldCard=card&&view.card===card&&current()?card:undefined;
  const writable=()=>current()&&!source.getSnapshot().busy&&!source.getSnapshot().uncertain&&source.getSnapshot().phase==='ready';
  const openEditor=(hook?:Webhook)=>{if(!writable()||hook&&!listed(hook))return;const next={...(hook?{hook}:{})};view.confirmation=undefined;setConfirmation(undefined);view.editor=next;setEditor(next);setName(hook?.name??'');setUrl(hook?.url??'');setEvents(hook?.events??[]);setEnabled(hook?.enabled??true);setSigning('');view.card=undefined;view.io=undefined;setCard(undefined);};
  const closeEditor=()=>{if(!heldEditor||view.editor!==heldEditor||!current())return;view.editor=undefined;setEditor(undefined);setSigning('');};
  const closeConfirmation=()=>{if(!heldConfirmation||view.confirmation!==heldConfirmation||!current())return;view.confirmation=undefined;setConfirmation(undefined);setReplacement('');};
  const closeCard=()=>{if(!heldCard||view.card!==heldCard)return;view.card=undefined;view.io=undefined;setCard(undefined);setCopied(false);setCopyError(false);};
  const showIssued=(issued:IssuedWebhook)=>{if(!issued.secret)return;const next={issued};view.card=next;setCard(next);setCopied(false);setCopyError(false);};
  const save=async()=>{const captured=heldEditor;if(!captured||!writable()||view.editor!==captured||!name.trim()||!webhookAddressOk(url)||!events.length)return;const draft=Object.freeze({name:name.trim(),url:url.trim(),events:Object.freeze([...events]),enabled}),secret=signing;setSigning('');let issued;if(captured.hook){/* A retired integration is reviewed by saving the destination and subscription together, even when one is unchanged; the bare enable never records review. */const changes=captured.hook.requiresOwnerReview?{...webhookEdits(captured.hook,draft),url:draft.url,events:draft.events}:webhookEdits(captured.hook,draft);issued=await source.update(captured.hook,changes);}else issued=await source.create({...draft,...(secret?{secret}:{})});if(!issued||!current()||view.editor!==captured)return;view.editor=undefined;setEditor(undefined);showIssued(issued);};
  const confirm=async()=>{const captured=heldConfirmation;if(!captured||!writable()||view.confirmation!==captured||!listed(captured.hook))return;const secret=replacement;setReplacement('');const result=await(captured.kind==='remove'?source.remove(captured.hook):source.update(captured.hook,webhookSecretChange(secret)));if(!result||!current()||view.confirmation!==captured)return;view.confirmation=undefined;setConfirmation(undefined);if(typeof result==='object')showIssued(result);};
  const cardIO=async(kind:'copy'|'share')=>{const captured=heldCard;if(!captured?.issued.secret||!current()||view.card!==captured||view.io)return;const io={};view.io=io;try{if(!current()||view.card!==captured||view.io!==io)return;if(kind==='copy')await copyText(captured.issued.secret);else await Share.share({title:captured.issued.webhook.name,message:captured.issued.secret});if(current()&&view.card===captured&&view.io===io){setCopied(kind==='copy');setCopyError(false);}}catch{if(current()&&view.card===captured&&view.io===io)setCopyError(true);}finally{if(view.io===io)view.io=undefined;}};
  const ask=(hook:Webhook,kind:Confirmation['kind'])=>{if(!writable()||!listed(hook))return;const next={hook,kind};setReplacement('');view.editor=undefined;setEditor(undefined);setSigning('');view.card=undefined;view.io=undefined;setCard(undefined);view.confirmation=next;setConfirmation(next);};
  const edit=()=>!!heldEditor&&view.editor===heldEditor&&writable();
  if (view.menu && current() && !listed(view.menu.hook)) view.menu = undefined;
  const heldMenu = menu && view.menu === menu && current() && listed(menu.hook) ? menu : undefined;
  const closeMenu = () => {if (!menu || view.menu !== menu) return; view.menu = undefined; setMenu(undefined);};
  const menuAction = (perform: (hook: Webhook) => void, write = true) => {
    if (!heldMenu || view.menu !== heldMenu || !current() || !listed(heldMenu.hook) || (write ? !writable() : source.getSnapshot().busy)) return;
    view.menu = undefined; setMenu(undefined); perform(heldMenu.hook);
  };
  const openLog = (hook: Webhook) => {if (!current() || !listed(hook)) return; const next = {hook}; view.log = next; setLog(next); void source.select(hook);};
  const refreshLog = () => {const captured = log; if (!captured || view.log !== captured || !current() || !listed(captured.hook) || source.getSnapshot().selected !== captured.hook) return; void source.select(captured.hook);};
  const closeLog = () => {if (!log || view.log !== log || !current()) return; view.log = undefined; setLog(undefined); source.close();};
  const menuItems: readonly MenuEntry[] = heldMenu ? [
    {id: 'edit', icon: 'i-edit', label: t('web.selection.edit'), disabled: !writable(), onSelect: () => menuAction(openEditor)},
    {id: 'test', icon: 'i-send', label: t('api.webhooks.sendTest'), disabled: !writable() || heldMenu.hook.requiresOwnerReview === true, onSelect: () => {if (heldMenu.hook.requiresOwnerReview !== true) menuAction(hook => {void source.test(hook);});}},
    {id: 'log', icon: 'i-logs', label: t('api.webhooks.log'), disabled: state.busy, onSelect: () => menuAction(openLog, false)},
    {id: 'secret', separator: true},
    {id: 'rotate', icon: 'i-refresh', label: t('api.webhooks.rotate'), disabled: !writable(), onSelect: () => menuAction(hook => ask(hook, 'rotate'))},
    {id: 'remove', icon: 'i-trash', label: t('web.missingTitles.removeConfirm'), destructive: true, disabled: !writable(), onSelect: () => menuAction(hook => ask(hook, 'remove'))},
  ] : [];

  return <View style={{marginTop:14,gap:12}} testID="server-integrations-webhooks">
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('api.webhooks.title')}</Text><Text style={{color:theme.colors.textSecondary}}>{t('api.webhooks.lede')}</Text>
    <Button size="compact" disabled={!writable()||!!state.list&&state.list.items.length>=state.list.maximum} onPress={()=>openEditor()}>{t('api.webhooks.add')}</Button>
    {(state.error||state.uncertain)&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(state.uncertain?'error.unconfirmedChange':'error.unavailable')}</Text>}
    <Button size="compact" variant="glass" disabled={state.busy} onPress={()=>{if(current())void(state.uncertain?source.reconcile():source.load());}}>{t('web.selection.refresh')}</Button>
    {state.phase==='loading'&&<Text style={{color:theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}{state.list?.items.length===0&&<Text style={{color:theme.colors.textSecondary}}>{t('api.webhooks.none')}</Text>}
    {state.list?.items.map(hook => {const trouble = webhookTrouble(t, hook), reviewPending = hook.requiresOwnerReview === true && source.reviewStep(hook) !== 'done'; return <View key={hook.id} style={{padding: 12, gap: 8, borderRadius: 12, backgroundColor: theme.colors.raised}}>
      <Focusable accessibilityRole="button" accessibilityLabel={hook.name} onPress={() => {if (current() && listed(hook) && !view.menu && !view.editor && !view.confirmation && !view.card && !view.log) {const next = {hook}; view.menu = next; setMenu(next);}}} style={{gap: 6}}>
        <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{hook.name}</Text>
        <Text style={{color: theme.colors.textSecondary, fontFamily: 'monospace'}}>{hook.url}</Text>
        <Text style={{color: theme.colors.textSecondary}}>{t('api.webhooks.eventCount', {count: hook.events.length})}</Text>
        {trouble && <Text style={{color: trouble.tone === 'danger' ? theme.colors.danger : trouble.tone === 'warning' ? theme.colors.amber : theme.colors.textSecondary}}>{trouble.text}</Text>}
      </Focusable>
      <Switch checked={hook.enabled} disabled={!writable() || reviewPending} onCheckedChange={value => {if (writable() && !reviewPending && listed(hook)) void source.update(hook, {enabled: value});}}>{t('api.webhooks.turnOn', {name: hook.name})}</Switch>
    </View>;})}
    <Menu open={!!heldMenu} onOpenChange={open => {if (!open) closeMenu();}} presentation="sheet" title={heldMenu?.hook.name} items={menuItems}/>

    <BottomSheet open={!!heldEditor} onOpenChange={value=>{if(!value)closeEditor();}} title={t(heldEditor?.hook?'web.selection.edit':'api.webhooks.add')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={closeEditor}>{t('action.cancel')}</Button><Button disabled={!writable()||!name.trim()||!webhookAddressOk(url)||!events.length} loading={state.busy} onPress={()=>{void save();}}>{t('action.save')}</Button></View>}><View style={{gap:12}}>{heldEditor?.hook?.requiresOwnerReview===true&&<Text style={[shellType(12.5,500,17),{color:theme.colors.amber}]}>{t('api.webhooks.ownerReviewRequired')}</Text>}<TextField label={t('builder.name')} value={name} maxLength={160} editable={!state.busy&&!state.uncertain} onChangeText={value=>{if(edit())setName(value);}}/><TextField label={t('api.webhooks.url')} value={url} maxLength={2048} editable={!state.busy&&!state.uncertain} autoCapitalize="none" autoCorrect={false} onChangeText={value=>{if(edit())setUrl(value);}}/>{!heldEditor?.hook&&<TextField label={t('api.webhooks.secret')} description={t('api.webhooks.secretHelp')} value={signing} maxLength={256} secureTextEntry editable={!state.busy&&!state.uncertain} onChangeText={value=>{if(edit())setSigning(value);}}/>}{webhookEventGroups(state.list?.events ?? []).map(eventGroup => <View key={eventGroup.id} style={{gap: 8}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Text accessibilityRole="header" style={[shellType(15, 700), {color: theme.colors.text, flex: 1}]}>{t(eventGroup.label)}</Text><Button variant="text" size="small" disabled={!writable()} onPress={() => {if (edit()) setEvents(old => selectWebhookEventGroup(old, eventGroup.events, true));}}>{t('api.webhooks.groupAll')}</Button><Button variant="text" size="small" disabled={!writable()} onPress={() => {if (edit()) setEvents(old => selectWebhookEventGroup(old, eventGroup.events, false));}}>{t('api.webhooks.groupNone')}</Button></View>
      {eventGroup.events.map(event => <Focusable key={event} accessibilityRole="checkbox" accessibilityState={{checked: events.includes(event), disabled: !writable()}} accessibilityLabel={webhookEventLabel(t, defaultI18n.has, event)} disabled={!writable()} onPress={() => {if (edit()) setEvents(old => selectWebhookEventGroup(old, [event], !old.includes(event)));}} style={{minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 12}}>
        <View accessible={false} style={{width: 20, height: 20, borderRadius: 5, borderWidth: 1, borderColor: events.includes(event) ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: events.includes(event) ? theme.colors.accentStrong : 'transparent', alignItems: 'center', justifyContent: 'center'}}>{events.includes(event) && <Icon name="i-check" size={16} color={theme.colors.ink}/>}</View><Text style={[shellType(14, 500), {color: theme.colors.text}]}>{webhookEventLabel(t, defaultI18n.has, event)}</Text>
      </Focusable>)}
    </View>)}</View></BottomSheet>
    <BottomSheet open={!!heldConfirmation} onOpenChange={value=>{if(!value)closeConfirmation();}} title={t(heldConfirmation?.kind==='rotate'?'api.webhooks.rotateTitle':'api.webhooks.removeTitle',{name:heldConfirmation?.hook.name??''})} description={t(heldConfirmation?.kind==='rotate'?'api.webhooks.rotateBody':'api.webhooks.removeBody')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={closeConfirmation}>{t('action.cancel')}</Button><Button variant={heldConfirmation?.kind==='remove'?'danger':'primary'} disabled={!writable()} loading={state.busy} onPress={()=>{void confirm();}}>{t(heldConfirmation?.kind==='rotate'?'api.webhooks.rotate':'web.missingTitles.removeConfirm')}</Button></View>}>{heldConfirmation?.kind==='rotate'?<TextField label={t('api.webhooks.secret')} description={t('api.webhooks.secretHelp')} value={replacement} maxLength={256} secureTextEntry editable={!state.busy&&!state.uncertain} onChangeText={value=>{if(writable()&&view.confirmation===heldConfirmation)setReplacement(value);}}/>:<View/>}</BottomSheet>
    <BottomSheet open={!!heldCard} dismissOnBack={false} dismissOnGesture={false} dismissOnOutsidePress={false} showClose={false} onOpenChange={value=>{if(!value)closeCard();}} title={t('api.webhooks.secretTitle')} description={t('api.webhooks.secretShownOnce')} footer={<View style={{gap:8}}><View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={()=>{void cardIO('copy');}}>{t('web.twoStep.copy')}</Button><Button variant="glass" onPress={()=>{void cardIO('share');}}>{t('api.tokens.share')}</Button></View><Button onPress={closeCard}>{t('web.access.copiedIt')}</Button></View>}><Text selectable style={{color:theme.colors.text}}>{heldCard?.issued.secret}</Text>{copied&&<Text style={{color:theme.colors.textSecondary}}>{t('api.credential.copied')}</Text>}{copyError&&<Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}</BottomSheet>
    <BottomSheet open={!!log && view.log === log && current() && listed(log.hook)} onOpenChange={open => {if (!open) closeLog();}} title={t('api.webhooks.log')} description={log?.hook.name} defaultDetent={1} footer={<View style={{flexDirection: 'row', gap: 8}}><Button onPress={closeLog}>{t('action.done')}</Button><Button variant="glass" onPress={refreshLog}>{t('web.selection.refresh')}</Button></View>}>
      <View style={{gap: 12}}>
        {state.selected === log?.hook && state.error === 'deliveries' && <View style={{gap: 8}}><Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t('error.unavailable')}</Text><Button variant="glass" onPress={refreshLog}>{t('action.tryAgain')}</Button></View>}
        {state.selected === log?.hook && !state.deliveries && state.error !== 'deliveries' && <Text style={{color: theme.colors.textSecondary}}>{t('next.settings.loading')}</Text>}
        {state.selected === log?.hook && state.deliveries?.items.length === 0 && state.error !== 'deliveries' && <Text style={[shellType(14), {color: theme.colors.textSecondary}]}>{t('api.webhooks.logEmpty')}</Text>}
        {state.selected === log?.hook && state.deliveries?.items.map(delivery => {const tone = deliveryTone(delivery), color = tone === 'healthy' ? theme.colors.healthy : tone === 'danger' ? theme.colors.record : theme.colors.amber; return <View key={delivery.id} style={{gap: 6, paddingVertical: 12, borderBottomWidth: 1, borderColor: theme.colors.lineSoft}}>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 12}}><Text style={[shellType(14, 700), {color: theme.colors.text, flex: 1}]}>{delivery.event === 'test' ? t('api.webhooks.testEvent') : webhookEventLabel(t, defaultI18n.has, delivery.event)}</Text><View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}><View style={{width: 6, height: 6, borderRadius: 3, backgroundColor: color}}/><Text style={[shellType(12.5, 600), {color}]}>{t(`api.webhooks.delivery.${delivery.state}`)}</Text></View></View>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}><Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{defaultI18n.dateTime(delivery.createdAt)}</Text><Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{deliveryProgress(t, delivery, iso => defaultI18n.time(Date.parse(iso)))}</Text></View>
          {deliveryProblem(t, defaultI18n.has, delivery) && <Text style={[shellType(13), {color: theme.colors.record}]}>{deliveryProblem(t, defaultI18n.has, delivery)}</Text>}
        </View>;})}
        {state.selected === log?.hook && state.deliveries?.nextCursor && <Button size="compact" onPress={() => {const snapshot = source.getSnapshot(); if (current() && view.log === log && snapshot.selected === state.selected && snapshot.deliveries === state.deliveries && state.selected && state.deliveries?.nextCursor) void source.select(state.selected, state.deliveries.nextCursor);}}>{t('action.showMore')}</Button>}
      </View>
    </BottomSheet>
  </View>;
}
