import type {PreferencesReader} from '../../../../../packages/client-core/src/preferences-reader';
import {validatePreferencePatch, type DeviceClass, type PreferencePatch, type PreferenceScope, type PreferenceSnapshot} from '../../../../../packages/client-core/src/preferences';
import {nativeServerPreferences, type NativeServerPreferencesOwner} from '../../platform/server-preferences';
import {NativeScreenLifetime, NativeScreenStore} from './lifetime';

export type NativeSettingsSnapshot = Readonly<{preferences: PreferenceSnapshot; busy: boolean; error?: unknown; saved: number}>;
export type NativeSettingsReader = Pick<PreferencesReader, 'preferences' | 'applyPreferences' | 'dispose'>;

/** Server defaults and where each setting is saved stay in the canonical preference facade. */
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
    this.publish({...this.getSnapshot(), preferences, error: undefined});
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
    if (!state.preferences.documents.some(value => value.scope === scope)) throw new Error('native_preferences_scope_missing');
    this.publish({...state, busy: true, error: undefined});
    try {
      const next = await this.reader.applyPreferences(scope, state.preferences.deviceClass, patch);
      this.lifetime.assert();
      if (this.disposed) return false;
      this.publish({preferences: next, busy: false, saved: this.getSnapshot().saved + 1});
      return true;
    } catch (error) {
      if (!this.disposed && this.lifetime.current()) this.publish({...this.getSnapshot(), busy: false, error});
      return false;
    }
  }
  dispose(): void {if (this.disposed) return; this.disposed = true; this.reader.dispose(); this.clearListeners();}
}

export const nativeSettingsDeviceClass = (isTV: boolean): DeviceClass => isTV ? 'television' : 'mobile';

/** A screen may retire its own scope, never the bootstrap's shared reader. */
export function createBoundNativeSettingsController(lifetime: NativeScreenLifetime, owner: NativeServerPreferencesOwner): NativeSettingsController {
  const source = nativeServerPreferences(owner);
  let unsubscribe = () => {};
  const controller = new NativeSettingsController(lifetime, {
    preferences: deviceClass => lifetime.request(() => source.preferences(deviceClass)),
    applyPreferences: (scope, deviceClass, values) => lifetime.request(() => source.applyPreferences(scope, deviceClass, values)),
    dispose: () => {unsubscribe(); lifetime.dispose();},
  }, source.getSnapshot());
  unsubscribe = source.subscribe(() => {if (lifetime.current()) controller.accept(source.getSnapshot());});
  return controller;
}
