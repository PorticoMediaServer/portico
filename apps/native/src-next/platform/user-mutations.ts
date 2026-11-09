import {parseAppSessionRecord} from '../../../../packages/client-core/src/app-sessions';
import {subscribeNativeAppSessions} from './app-session-storage';
import {NativeQueueStore} from './queue-storage';
import {NativeUserMutationSelectionOwner} from './user-mutation-selection';
import {NativeModules} from 'react-native';
import {nativeStopScope, type NativeStopScope} from './native-stop-scope';
export {nativeStopScope, sameNativeStopScope, type NativeStopScope} from './native-stop-scope';

/**
 * The system audio player (Kotlin and Objective-C) sends its own stop when listening ends from the
 * lock screen or a notification, with no JavaScript running. It still speaks the present server's
 * older stop protocol, and keeps its stops in a native store of its own. This file is the little
 * that store needs from JavaScript: which sign-in is active, and clearing out stops that are done.
 * Everything else a person does while out of reach goes through the outbox (`outbox-store.ts`).
 * This goes when the native players send a plain stop.
 */
export interface NativeUserMutationBridge {
  userMutationTransaction(operation: 'read' | 'cas' | 'scope' | 'retireScope', payload: string): Promise<string | boolean>;
}
export class NativeStopStore {
  constructor(private readonly bridge: NativeUserMutationBridge = NativeModules.PorticoRuntime) {}
  private call(operation: 'read' | 'cas' | 'scope' | 'retireScope', payload: unknown): Promise<string | boolean> {
    if (typeof this.bridge?.userMutationTransaction !== 'function') return Promise.reject(new Error('The system player’s stop store is unavailable.'));
    return this.bridge.userMutationTransaction(operation, JSON.stringify(payload));
  }
  /** The native players send a stop only for the sign-in made active here. */
  async activate(scope: NativeStopScope): Promise<void> { await this.call('scope', {scope}); }
  async retire(scope: NativeStopScope): Promise<boolean> { return await this.call('retireScope', {scope}) === true; }
  /** Drops every stop that is no longer waiting to be sent, so the native store never fills. */
  async clearFinished(): Promise<void> {
    const state = JSON.parse(String(await this.call('read', {}))) as {revision: number; entries: {state: string}[]};
    const waiting = state.entries.filter(entry => entry.state === 'pending');
    if (waiting.length !== state.entries.length) await this.call('cas', {expectedRevision: state.revision, next: {...state, revision: state.revision + 1, entries: waiting}});
  }
}

// One observer per JS app lifetime, deliberately independent of shell mounts.
let selectionOwner: NativeUserMutationSelectionOwner | undefined;
export function retainNativeStopScope(store: NativeStopStore, scope: NativeStopScope, report: (error: unknown) => void): Promise<void> {
  selectionOwner ??= new NativeUserMutationSelectionOwner(work => new NativeQueueStore().withRecordLease('accounts.sessions.v1', async read => {
    const record = parseAppSessionRecord(await read()), selected = record.servers.find(value => value.connection.pin.serverId === record.selectedServerId);
    return work(selected && nativeStopScope(selected.connection.logicalOrigin, selected.connection.session));
  }), subscribeNativeAppSessions);
  return selectionOwner.activate(store, scope, report);
}
