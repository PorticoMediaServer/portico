import {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {ConsoleClient, type ConsoleAPI} from '../../../../../../../packages/client-core/src/console';
import {isAuthenticationFailure} from '../../../../../../../packages/client-core/src/backups';
import {FormSet, type DraftForm, type FormState} from '../../../../../../../packages/client-core/src/server-admin/draft-form';
import {createServerForm, type ServerFormDeps, type ServerFormId} from '../../../../../../../packages/client-core/src/server-admin/server-forms';
import {nativeRouteCrypto} from '../../../../platform/server-connections';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from './space-source';

const operationHex = (): string => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');
let nextMount = 0;
export type StorageForms = Readonly<{
  mount: number;
  context: NativeShellBootstrapContext;
  forms: Readonly<Record<ServerFormId, DraftForm<any, any>>>;
  set: FormSet;
  dispose: () => void;
}>;
export function createStorageForms(context: NativeShellBootstrapContext, ids: readonly ServerFormId[], reportError: NativeShellRuntime['reportError']): StorageForms {
  const lifetime = new NativeScreenLifetime(context), set = new FormSet(), forms: Partial<Record<ServerFormId, DraftForm<any, any>>> = {}, offs: (() => void)[] = [];
  let consoleClient: ConsoleClient | undefined;
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const dispose = () => {lifetime.dispose();for (const form of Object.values(forms)) form.dispose();for (const off of offs) off();consoleClient?.dispose();};
  const originalApi = context.api;
  const requireCurrent = () => {if (!current()) throw screenScopeEnded();};
  const api: ConsoleAPI = {
    ...(originalApi.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
      requireCurrent(); await originalApi.acknowledgeUserMutation(id); requireCurrent();
    }} : {}),
    ...(originalApi.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
      requireCurrent(); await originalApi.awaitUserMutationsReady(); requireCurrent();
    }} : {}),
    ...(originalApi.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
      requireCurrent(); const value = originalApi.getUserMutationCoordinator(); requireCurrent(); return value;
    }} : {}),
    ...(originalApi.getUserMutationScope ? {getUserMutationScope: () => {
      requireCurrent(); const value = originalApi.getUserMutationScope(); requireCurrent(); return value;
    }} : {}),
    ...(originalApi.requestConsole ? {requestConsole: async <T,>(path: string, method: string, body: unknown, caller: AbortSignal): Promise<T> => {
      requireCurrent();
      try {
        const value = await lifetime.request(signal => originalApi.requestConsole<T>(path, method, body, signal), caller);
        requireCurrent(); return value;
      } catch (error) {if (isAuthenticationFailure(error)) {const owned = current();dispose();if (owned) reportError(error);}throw error;}
    }} : {}),
    request: async <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => {
    if (!current()) throw screenScopeEnded();
    try {const value = await lifetime.request(signal => originalApi.request<T>(path, method, body, signal), caller);requireCurrent();return value;}
    catch (error) {if (isAuthenticationFailure(error)) {const owned = current();dispose();if (owned) reportError(error);}throw error;}
  }};
  const authority = context.authority();
  consoleClient = new ConsoleClient(api, {serverId: authority.serverId, viewerId: authority.profileId});
  const deps: ServerFormDeps = {api, console: consoleClient, serverId: authority.serverId, operationId: operationHex};
  for (const id of ids) {const form = createServerForm(id, deps);forms[id] = form;offs.push(set.add(id, form));}
  return {mount: ++nextMount, context, forms: forms as Record<ServerFormId, DraftForm<any, any>>, set, dispose};
}
/** Every committed owner viewport receives fresh forms and a captured transport.
 * Cleanup never revives disposed forms during a later setup. */
export function useStorageForms(ports: NativeSettingsLivePorts, ids: readonly ServerFormId[], reportError: NativeShellRuntime['reportError']): StorageForms | undefined {
  const context = ports.context, [group, setGroup] = useState<StorageForms>();
  useLayoutEffect(() => {
    const next = createStorageForms(context, ids, reportError);setGroup(next);
    for (const form of Object.values(next.forms)) void form.load();
    return next.dispose;
  }, [context, ids, reportError]);
  return group?.context === context ? group : undefined;
}
export function useStorageSet(group: StorageForms) {return useSyncExternalStore(group.set.subscribe, group.set.getSnapshot, group.set.getSnapshot);}
export function useFormState<T extends object, S>(form: DraftForm<any, any>): FormState<T, S> {
  return useSyncExternalStore(form.subscribe, form.getSnapshot) as FormState<T, S>;
}
