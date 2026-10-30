import {validatePreferencePatch, type DeviceClass, type PreferencePatch, type PreferenceScope, type PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';

export type NativeServerPreferencesOwner = Readonly<{
  current: () => boolean; preferences: () => PreferenceSnapshot;
  subscribePreferences: (listener: () => void) => () => void;
}>;
type Ports = Readonly<{
  read: (deviceClass: DeviceClass) => Promise<PreferenceSnapshot>;
  apply: (scope: PreferenceScope, deviceClass: DeviceClass, revision: number, values: PreferencePatch) => Promise<PreferenceSnapshot>;
  accept: (next: PreferenceSnapshot) => PreferenceSnapshot;
}>;
const sources = new WeakMap<NativeServerPreferencesOwner, NativeServerPreferences>();
const ended = () => Object.assign(new Error('native_server_preferences_retired'), {name: 'AbortError'});

/** One bootstrap reader and confirmed snapshot. Borrowers own neither its cache
 * nor its lifetime; writes retain the caller's exact server CAS revision. */
export class NativeServerPreferences {
  private readonly owner: NativeServerPreferencesOwner;
  private readonly ports: Ports;
  constructor(owner: NativeServerPreferencesOwner, ports: Ports) {this.owner = owner; this.ports = ports;}
  private assert(deviceClass?: DeviceClass): void {
    if (!this.owner.current()) throw ended();
    if (deviceClass !== undefined && deviceClass !== this.owner.preferences().deviceClass) throw new Error('native_server_preferences_device_class_mismatch');
  }
  getSnapshot = (): PreferenceSnapshot => {this.assert(); return this.owner.preferences();};
  subscribe = (listener: () => void): (() => void) => {
    this.assert(); return this.owner.subscribePreferences(() => {if (this.owner.current()) listener();});
  };
  preferences = async (deviceClass: DeviceClass): Promise<PreferenceSnapshot> => {
    this.assert(deviceClass);
    const next = await this.ports.read(deviceClass);
    this.assert(deviceClass);
    if (next.deviceClass !== deviceClass) throw new Error('native_server_preferences_device_class_mismatch');
    return this.ports.accept(next);
  };
  applyPreferences = async (scope: PreferenceScope, deviceClass: DeviceClass, revision: number, values: PreferencePatch): Promise<PreferenceSnapshot> => {
    this.assert(deviceClass);
    const snapshot = this.owner.preferences();
    if (!snapshot.documents.some(document => document.scope === scope)) throw new Error('native_preferences_scope_missing');
    const patch = validatePreferencePatch(snapshot.registry, scope, values);
    const next = await this.ports.apply(scope, deviceClass, revision, patch);
    this.assert(deviceClass);
    if (next.deviceClass !== deviceClass) throw new Error('native_server_preferences_device_class_mismatch');
    return this.ports.accept(next);
  };
}

export function bindNativeServerPreferences(owner: NativeServerPreferencesOwner, ports: Ports): NativeServerPreferences {
  if (!owner.current()) throw ended();
  if (sources.has(owner)) throw new Error('native_server_preferences_already_bound');
  const source = new NativeServerPreferences(owner, ports); sources.set(owner, source); return source;
}
export function nativeServerPreferences(owner: NativeServerPreferencesOwner): NativeServerPreferences {
  if (!owner.current()) throw ended();
  const source = sources.get(owner);
  if (!source) throw Object.assign(new Error('native_server_preferences_unavailable'), {code: 'unsupported'});
  return source;
}
