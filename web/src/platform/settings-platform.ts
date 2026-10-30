import type {ComponentType} from 'react';
import type {SettingsEnvironment} from '@core/presentation/settings-visibility.ts';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {EqualizerPort} from '../kit/media/equalizer-port';

export type SettingsViewerScope = Readonly<{
  serverId: string; authority: 'local' | 'hosted'; accountId: string; profileId: string; sessionFamilyId: string;
}>;
/** A real app/device-local facade document. Its keys are resolved by settingsBinding, never by the UI. */
export type SettingsDeviceDocument = Readonly<{
  revision: string;
  values: Readonly<Record<string, SettingsValue>>;
  defaults: Readonly<Record<string, SettingsValue>>;
}>;
export type SettingsDeviceFacts = Readonly<{
  name?: Readonly<{value: string; readOnly: boolean}>;
  appVersion?: string;
  update?: 'up-to-date' | 'ready';
}>;
export type SettingsDevicePort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Readonly<{facts: SettingsDeviceFacts; document?: SettingsDeviceDocument}>;
  /** Absent for a facts-only adapter. A document without a setter is read-only. */
  apply?: (expectedRevision: string, patch: Readonly<Record<string, SettingsValue>>, signal: AbortSignal) => Promise<void>;
  restartToUpdate?: () => Promise<void>;
}>;
export type SettingsDownloadsSnapshot = Readonly<{available: boolean; freeBytes?: number}>;
/** This door is viewer-bound. Server preparation is not evidence of local storage or an offline screen. */
export type SettingsDownloadsPort = Readonly<{
  scope: SettingsViewerScope;
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => SettingsDownloadsSnapshot;
  open: (signal: AbortSignal) => Promise<void>;
  dispose: () => void;
}>;
export type SettingsPlatform = Readonly<{
  environment: SettingsEnvironment;
  device?: SettingsDevicePort;
  bindDownloads?: (scope: SettingsViewerScope, isCurrent: () => boolean) => SettingsDownloadsPort;
  /** The media owner binds its one app/device store and actual controlled EQ component. */
  Equalizer?: ComponentType<{layout: 'settings'; onError: (error: unknown) => void}>;
  /** The same store the Equalizer edits, so the row can show the real On/Off state.
   * A browser owns its renderer store and leaves this absent; a native host binds it. */
  equalizerPort?: EqualizerPort;
}>;

let installed: SettingsPlatform | undefined;
/** Install before shell restoration, like installPlaybackEnvironment. The caller owns native resources. */
export function installSettingsPlatform(platform: SettingsPlatform): () => void {
  installed = platform;
  return () => { if (installed === platform) installed = undefined; };
}
export function settingsPlatform(): SettingsPlatform | undefined { return installed; }

const unavailable: SettingsDownloadsSnapshot = Object.freeze({available: false});
const sameScope = (a: SettingsViewerScope, b: SettingsViewerScope) => (['serverId', 'authority', 'accountId', 'profileId', 'sessionFamilyId'] as const).every(key => a[key] === b[key]);

/** Synchronous authority erasure and request cancellation at the shared shell boundary. */
export function bindSettingsDownloads(platform: SettingsPlatform | undefined, scope: SettingsViewerScope, isCurrent: () => boolean): SettingsDownloadsPort | undefined {
  if (!platform?.bindDownloads || !isCurrent()) return undefined;
  const captured = Object.freeze({...scope}), port = platform.bindDownloads(captured, isCurrent);
  if (!sameScope(port.scope, captured)) { port.dispose(); throw new Error('settings_download_scope_mismatch'); }
  const lifetime = new AbortController(), subscriptions = new Set<() => void>();
  let closed = false;
  const current = () => !closed && isCurrent() && sameScope(port.scope, captured);
  return Object.freeze({
    scope: captured,
    getSnapshot: () => current() ? port.getSnapshot() : unavailable,
    subscribe(listener: () => void) {
      if (!current()) return () => {};
      const off = port.subscribe(() => { if (current()) listener(); });
      subscriptions.add(off);
      return () => { subscriptions.delete(off); off(); };
    },
    async open(signal: AbortSignal) {
      if (!current()) throw new Error('settings_download_scope_closed');
      const combined = AbortSignal.any([signal, lifetime.signal]);
      combined.throwIfAborted();
      await port.open(combined);
      combined.throwIfAborted();
      if (!current()) throw new Error('settings_download_scope_closed');
    },
    dispose() {
      if (closed) return;
      closed = true; lifetime.abort();
      for (const off of subscriptions) off();
      subscriptions.clear(); port.dispose();
    },
  });
}
