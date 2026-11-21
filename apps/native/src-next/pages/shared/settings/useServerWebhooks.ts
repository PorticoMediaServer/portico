import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {selectWebhookEventGroup} from '../../../screens/settings-live/server/integrations/webhook-form';
import {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Share} from 'react-native';
import {SettingsWebhooksSource} from '../../../../../../packages/client-core/src/server-admin/webhooks-source';
import {webhookEdits, type Webhook, type IssuedWebhook, type WebhookTest} from '../../../../../../packages/client-core/src/api-access';
import {webhookAddressOk} from '../../../../../../packages/client-core/src/server-admin/api-words';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';
import {copyText} from '../../../platform/clipboard';
import type {MenuEntry} from '../../../kit/overlays';

function createWebhooks(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request:<T,>(path:string,method?:string,body?:unknown,caller?:AbortSignal):Promise<T> => current() ? lifetime.request(signal=>context.api.request<T>(path,method,body,signal),caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsWebhooksSource(withGuardedRaw(api, context.api, () => {if (!current()) throw screenScopeEnded();}),current,error=>{const owned=current();source.dispose();lifetime.dispose();if(owned)reportError(error);});
  return {context,source,current,dispose:()=>{source.dispose();lifetime.dispose();}};
}
export type WebhookGroup = ReturnType<typeof createWebhooks>;
type Editor = Readonly<{hook?:Webhook}>;
type Confirmation = Readonly<{hook:Webhook;kind:'remove'|'rotate'}>;
type Card = Readonly<{issued:IssuedWebhook}>;
type ActionMenu = Readonly<{hook:Webhook}>;
export function useWebhookGroup({ports,runtime}:{ports:NativeSettingsLivePorts;runtime:NativeShellRuntime}) {
  const context=ports.context,reportError=runtime.reportError,[group,setGroup]=useState<WebhookGroup>();
  useLayoutEffect(()=>{const next=createWebhooks(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  return group?.context === context ? group : undefined;
}
export function useServerWebhooks({group,runtime}:{group:WebhookGroup;runtime:NativeShellRuntime}) {
  const {source}=group,t=runtime.text.t as typeof defaultI18n.t,state=useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot);
  const [editor,setEditor]=useState<Editor>(),[confirmation,setConfirmation]=useState<Confirmation>(),[card,setCard]=useState<Card>(),[name,setName]=useState(''),[url,setUrl]=useState(''),[events,setEvents]=useState<readonly string[]>([]),[copied,setCopied]=useState(false),[copyError,setCopyError]=useState(false),[log,setLog]=useState<Readonly<{hook:Webhook}>>(),[menu,setMenu]=useState<ActionMenu>(),[testResult,setTestResult]=useState<WebhookTest>();
  const view=useMemo(()=>({source,active:true,editor:undefined as Editor|undefined,confirmation:undefined as Confirmation|undefined,card:undefined as Card|undefined,io:undefined as object|undefined,log:undefined as Readonly<{hook:Webhook}>|undefined,menu:undefined as ActionMenu|undefined}),[source]);
  useLayoutEffect(()=>{setTestResult(undefined);view.active=true;return()=>{view.active=false;view.editor=undefined;view.confirmation=undefined;view.card=undefined;view.io=undefined;view.log=undefined;view.menu=undefined;};},[view]);
  useEffect(()=>{void source.load();},[source]);
  const current=()=>view.active&&view.source===source&&group.current()&&source.getSnapshot().phase!=='idle';
  const listed=(hook:Webhook)=>!!source.getSnapshot().list?.items.includes(hook);
  const heldEditor=editor&&view.editor===editor&&current()&&(!editor.hook||listed(editor.hook))?editor:undefined;
  const heldConfirmation=confirmation&&view.confirmation===confirmation&&current()&&listed(confirmation.hook)?confirmation:undefined;
  const heldCard=card&&view.card===card&&current()?card:undefined;
  const writable=()=>current()&&!source.getSnapshot().busy&&!source.getSnapshot().uncertain&&source.getSnapshot().phase==='ready';
  const openEditor=(hook?:Webhook)=>{if(!writable()||hook&&!listed(hook))return;const next={...(hook?{hook}:{})};view.confirmation=undefined;setConfirmation(undefined);view.editor=next;setEditor(next);setName(hook?.name??'');setUrl(hook?.url??'');setEvents(hook?.events??[]);view.card=undefined;view.io=undefined;setCard(undefined);};
  const closeEditor=()=>{if(!heldEditor||view.editor!==heldEditor||!current())return;view.editor=undefined;setEditor(undefined);};
  const closeConfirmation=()=>{if(!heldConfirmation||view.confirmation!==heldConfirmation||!current())return;view.confirmation=undefined;setConfirmation(undefined);};
  const closeCard=()=>{if(!heldCard||view.card!==heldCard)return;view.card=undefined;view.io=undefined;setCard(undefined);setCopied(false);setCopyError(false);};
  const showIssued=(issued:IssuedWebhook)=>{if(!issued.secret)return;const next={issued};view.card=next;setCard(next);setCopied(false);setCopyError(false);};
  const save=async()=>{const captured=heldEditor;if(!captured||!writable()||view.editor!==captured||!name.trim()||!webhookAddressOk(url)||!events.length)return;const draft=Object.freeze({name:name.trim(),url:url.trim(),events:Object.freeze([...events])});const issued=captured.hook?await source.update(captured.hook,webhookEdits(captured.hook,draft)):await source.create(draft);if(!issued||!current()||view.editor!==captured)return;view.editor=undefined;setEditor(undefined);if('webhook' in issued)showIssued(issued);};
  const confirm=async()=>{const captured=heldConfirmation;if(!captured||!writable()||view.confirmation!==captured||!listed(captured.hook))return;const result=await(captured.kind==='remove'?source.remove(captured.hook):source.rotate(captured.hook));if(!result||!current()||view.confirmation!==captured)return;view.confirmation=undefined;setConfirmation(undefined);if(typeof result==='object')showIssued(result);};
  const cardIO=async(kind:'copy'|'share')=>{const captured=heldCard;if(!captured?.issued.secret||!current()||view.card!==captured||view.io)return;const io={};view.io=io;try{if(!current()||view.card!==captured||view.io!==io)return;if(kind==='copy')await copyText(captured.issued.secret);else await Share.share({title:captured.issued.webhook.name,message:captured.issued.secret});if(current()&&view.card===captured&&view.io===io){setCopied(kind==='copy');setCopyError(false);}}catch{if(current()&&view.card===captured&&view.io===io)setCopyError(true);}finally{if(view.io===io)view.io=undefined;}};
  const ask=(hook:Webhook,kind:Confirmation['kind'])=>{if(!writable()||!listed(hook))return;const next={hook,kind};view.editor=undefined;setEditor(undefined);view.card=undefined;view.io=undefined;setCard(undefined);view.confirmation=next;setConfirmation(next);};
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
    {id: 'test', icon: 'i-send', label: t('api.webhooks.sendTest'), disabled: !writable(), onSelect: () => menuAction(hook => {setTestResult(undefined);void source.test(hook).then(out => {if (out && current()) setTestResult(out);});})},
    {id: 'log', icon: 'i-logs', label: t('api.webhooks.log'), disabled: state.busy, onSelect: () => menuAction(openLog, false)},
    {id: 'secret', separator: true},
    {id: 'rotate', icon: 'i-refresh', label: t('api.webhooks.rotate'), disabled: !writable(), onSelect: () => menuAction(hook => ask(hook, 'rotate'))},
    {id: 'remove', icon: 'i-trash', label: t('web.missingTitles.removeConfirm'), destructive: true, disabled: !writable(), onSelect: () => menuAction(hook => ask(hook, 'remove'))},
  ] : [];

  const enable = (hook: Webhook, enabled: boolean) => {if (writable() && listed(hook)) void source.update(hook, {enabled});};
  const openMenu = (hook: Webhook) => {
    if (!current() || !listed(hook) || view.menu || view.editor || view.confirmation || view.card || view.log) return;
    const next = {hook};
    view.menu = next;
    setMenu(next);
  };
  const more = () => {
    const snapshot = source.getSnapshot();
    if (current() && view.log === log && snapshot.selected === state.selected && snapshot.deliveries === state.deliveries && state.selected && state.deliveries?.nextCursor) {
      void source.select(state.selected, state.deliveries.nextCursor);
    }
  };
  const toggleEvent = (event: string) => {
    if (edit()) setEvents(old => selectWebhookEventGroup(old, [event], !old.includes(event)));
  };
  const toggleGroup = (groupEvents: readonly string[], selected: boolean) => {
    if (edit()) setEvents(old => selectWebhookEventGroup(old, groupEvents, selected));
  };
  return {
    state, testResult, writable: writable(), canSave: writable() && !!name.trim() && webhookAddressOk(url) && !!events.length,
    heldMenu, menuItems, openMenu, closeMenu, enable, openEditor, heldEditor, closeEditor, name, url, events, toggleEvent, toggleGroup,
    changeName: (value: string) => {if (edit()) setName(value);}, changeUrl: (value: string) => {if (edit()) setUrl(value);}, save,
    heldConfirmation, closeConfirmation, confirm, heldCard, closeCard, cardIO, copied, copyError,
    log: log && view.log === log && current() && listed(log.hook) ? log : undefined, closeLog, refreshLog, more,
    refresh: () => {if (current()) void (state.uncertain ? source.reconcile() : source.load());},
  };
}
