import type {SetupDraft, SetupDraftStore} from '../../../../packages/client-core/src/setup-onboarding';
import {NativeQueueStore} from './queue-storage';

/** Setup secrets use the protected native record store, never preferences or Git. */
export type NativeSetupDraftStore = Omit<SetupDraftStore, 'change'> & {change(update: (old: SetupDraft | undefined) => SetupDraft | undefined, beforeCommit?: () => void): Promise<void>};
export function nativeSetupDraft(serverId: string): NativeSetupDraftStore {
  const key = 'initialize:' + serverId, storage = new NativeQueueStore();
  const parse = (raw: unknown): SetupDraft | undefined => {
    if (raw == null) return undefined;
    const value = raw as Partial<SetupDraft>;
    if (value.version !== 1 || value.serverId !== serverId || typeof value.setupToken !== 'string' || !value.setupToken
      || typeof value.requestId !== 'string' || !/^[a-f0-9-]{36}$/.test(value.requestId)
      || typeof value.name !== 'string' || !['local', 'hosted'].includes(value.authMode ?? '')
      || (value.username !== undefined && typeof value.username !== 'string')) throw new Error('Invalid saved setup.');
    return value as SetupDraft;
  };
  return {
    read: () => storage.change(key, raw => ({record: raw, value: parse(raw)})),
    change: async (update, beforeCommit) => {await storage.change(key, raw => ({record: update(parse(raw)) ?? null, value: undefined}), undefined, beforeCommit);},
  };
}
