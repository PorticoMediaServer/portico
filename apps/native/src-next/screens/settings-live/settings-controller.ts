import {PreferencesReader, type PreferencesApi} from '../../../../../packages/client-core/src/preferences-reader';
import {currentPreferenceSnapshot, validatePreferencePatch, type DeviceClass, type PreferencePatch, type PreferenceScope, type PreferenceSnapshot} from '../../../../../packages/client-core/src/preferences';
import {nativeServerPreferences, type NativeServerPreferencesOwner} from '../../platform/server-preferences';
import {NativeScreenLifetime, NativeScreenStore} from './lifetime';

export type NativeSettingsSnapshot = Readonly<{preferences: PreferenceSnapshot; busy: boolean; error?: unknown; saved: number}>;
export type NativeSettingsReader = Pick<PreferencesReader, 'preferences' | 'applyPreferences' | 'dispose'>;

/** Server defaults, scope eligibility and revisions stay in the canonical preference facade. */
export class NativeSettingsController extends NativeScreenStore<NativeSettingsSnapshot> {
  private disposed = false;
  readonly lifetime: NativeScreenLifetime;
  private reader: NativeSettingsReader;
  constructor(lifetime: NativeScreenLifetime, reader: NativeSettingsReader, initial: PreferenceSnapshot) {
    super({preferences: initial, busy: false, saved: 0});
    this.lifetime = lifetime; this.reader = reader;
  }
  accept(preferences: PreferenceSnapshot): void {
    if (this.disposed || !this.lifetime.current()) return;
    this.publish({...this.getSnapshot(), preferences: currentPreferenceSnapshot(this.getSnapshot().preferences, preferences), error: undefined});
  }
  async refresh(): Promise<void> {
    this.lifetime.assert();
    try {this.accept(await this.reader.preferences(this.getSnapshot().preferences.deviceClass));}
    catch (error) {if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), error});}
  }
  async apply(scope: PreferenceScope, values: PreferencePatch): Promise<boolean> {
    this.lifetime.assert();
    const state = this.getSnapshot();
    if (state.busy || this.disposed) return false;
    const patch = validatePreferencePatch(state.preferences.registry, scope, values);
    const document = state.preferences.documents.find(value => value.scope === scope);
    if (!document) throw new Error('native_preferences_scope_missing');
    this.publish({...state, busy: true, error: undefined});
    try {
      const next = await this.reader.applyPreferences(scope, state.preferences.deviceClass, document.revision, patch);
      this.lifetime.assert();
      if (this.disposed) return false;
      this.publish({preferences: currentPreferenceSnapshot(this.getSnapshot().preferences, next), busy: false, saved: this.getSnapshot().saved + 1});
      return true;
    } catch (error) {
      if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), busy: false, error});
      return false;
    }
  }
  dispose(): void {if (this.disposed) return; this.disposed = true; this.reader.dispose(); this.clearListeners();}
}

export function createNativeSettingsController(lifetime: NativeScreenLifetime, api: PreferencesApi, initial: PreferenceSnapshot): NativeSettingsController {
  const scoped: PreferencesApi = {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => api.request<T>(path, method, body, next), signal)};
  return new NativeSettingsController(lifetime, new PreferencesReader(scoped, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId}), initial);
}

export const nativeSettingsDeviceClass = (isTV: boolean): DeviceClass => isTV ? 'television' : 'mobile';

/** A screen may retire its own scope, never the bootstrap's shared reader. */
export function createBoundNativeSettingsController(lifetime: NativeScreenLifetime, owner: NativeServerPreferencesOwner): NativeSettingsController {
  const source = nativeServerPreferences(owner);
  let unsubscribe = () => {};
  const controller = new NativeSettingsController(lifetime, {
    preferences: deviceClass => lifetime.request(() => source.preferences(deviceClass)),
    applyPreferences: (scope, deviceClass, revision, values) => lifetime.request(() => source.applyPreferences(scope, deviceClass, revision, values)),
    dispose: () => {unsubscribe(); lifetime.dispose();},
  }, source.getSnapshot());
  unsubscribe = source.subscribe(() => {if (lifetime.current()) controller.accept(source.getSnapshot());});
  return controller;
}
