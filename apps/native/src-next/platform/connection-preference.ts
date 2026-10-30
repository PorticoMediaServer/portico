import {Platform} from 'react-native';
import {parseInsecureConnections, type ConnectionPolicy, type InsecureConnections} from '../../../../packages/client-core/src/connection-policy';
import {NativeQueueStore} from './queue-storage';

/**
 * Settings › This device › Insecure connections, for the connection race (P2-69). Every race reads
 * it synchronously, so it is loaded once at start and kept current by the Settings write. Until it
 * has loaded, the race behaves as Never: a cold start can only be stricter than the setting.
 */
let current: InsecureConnections | undefined;
let loading: Promise<void> | undefined;

export function loadConnectionPreference(): Promise<void> {
  loading ??= new NativeQueueStore().change('preferences:v1', raw => ({record: raw, value: raw}))
    .then(raw => {if (current === undefined) current = parseInsecureConnections((raw as {allowInsecure?: unknown} | null)?.allowInsecure);})
    .catch(() => {if (current === undefined) current = parseInsecureConnections(undefined);});
  return loading;
}

/** The Settings write's committed value. */
export function acceptConnectionPreference(value: unknown) {current = parseInsecureConnections(value);}

/** Apple has no remote plain HTTP (clamped in client-core); every other native platform follows the setting. */
export function nativeConnectionPolicy(): ConnectionPolicy {
  return {insecure: current ?? 'never', platform: Platform.OS === 'ios' ? 'apple' : 'native'};
}
