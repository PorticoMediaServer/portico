import {passthroughFormats, readPassthroughFormats, type PassthroughFormat} from './playback/passthrough-formats';
import type {CompositeId} from '../../../../packages/client-core/src/presentation/settings-structure';
import {engineSettings, parseEngineSetting} from './playback/engine-choice';
import type {NativeQueueStore} from './queue-storage';
import {insecureChoicesFor, parseInsecureConnections} from '../../../../packages/client-core/src/connection-policy';
import {acceptConnectionPreference, nativeConnectionPolicy} from './connection-preference';

export type NativePlaybackDeviceSetting = 'playerEngine' | 'forceStereo' | 'disablePassthrough' | 'disableHdr' | 'tvHdr' | 'tvSurround' | 'music.sampleRateMatching' | 'lossNotices' | 'receiveFromDevices' | 'matchFrameRate' | 'matchDynamicRange' | 'allowInsecure';
type Owner = Readonly<{current: () => boolean; subscribePreferences: (listener: () => void) => () => void}>;
type Ports = Readonly<{storage: Pick<NativeQueueStore, 'change'>; readRecord: () => unknown; acceptRecord: (raw: unknown) => void}>;
const sources = new WeakMap<Owner, NativeDeviceSettings>();
const object = (raw: unknown): raw is Record<string, unknown> => !!raw && typeof raw === 'object' && !Array.isArray(raw);
const ended = () => Object.assign(new Error('native_device_settings_retired'), {name: 'AbortError'});
export function nativeDeviceSettingSupported(id: string): id is NativePlaybackDeviceSetting {
  return id === 'playerEngine' || id === 'forceStereo' || id === 'disablePassthrough' || id === 'disableHdr' || id === 'tvHdr' || id === 'tvSurround' || id === 'music.sampleRateMatching' || id === 'lossNotices' || id === 'receiveFromDevices' || id === 'matchFrameRate' || id === 'matchDynamicRange' || id === 'allowInsecure';
}

/** The installation fields consumed by the actual playback runtime. The
 * bootstrap's committed record is the only snapshot; registry rows are not local shadows. */
export class NativeDeviceSettings {
  private readonly owner: Owner;
  private readonly ports: Ports;
  constructor(owner: Owner, ports: Ports) {this.owner = owner; this.ports = ports;}
  private assert() {if (!this.owner.current()) throw ended();}
  supports = nativeDeviceSettingSupported;
  read = (id: CompositeId | 'lossNotices' | 'matchFrameRate' | 'matchDynamicRange' | 'allowInsecure'): string | boolean | undefined => {
    this.assert(); if (!nativeDeviceSettingSupported(id)) return;
    const raw = this.ports.readRecord(), record = object(raw) ? raw : {};
    if (id === 'matchFrameRate' || id === 'matchDynamicRange') return record[id] === 'off' ? 'off' : 'auto';
    if (id === 'allowInsecure') return parseInsecureConnections(record[id]);
    if (id === 'playerEngine') return parseEngineSetting(record.playerEngine);
    if (id === 'music.sampleRateMatching') return record[id] === 'strict' || record[id] === 'off' ? record[id] as string : record[id] === 'smart' ? 'smart' : undefined;
    // The TV output choices are tri-state: an absent key means the viewer has
    // never overridden what the TV reports, which is not the same as Off.
    if (id === 'tvHdr' || id === 'tvSurround') return typeof record[id] === 'boolean' ? record[id] as boolean : undefined;
    return (id === 'lossNotices' || id === 'receiveFromDevices') ? record[id] !== false : record[id] === true;
  };
  readPassthroughFormat = (format: PassthroughFormat): 'auto' | 'off' => {
    this.assert(); const raw = this.ports.readRecord();
    if (object(raw) && raw.disablePassthrough === true) return 'off';
    return readPassthroughFormats(object(raw) ? raw.passthroughFormats : undefined)[format] ?? 'auto';
  };
  writePassthroughFormat = async (format: PassthroughFormat | 'all', value: 'auto' | 'off'): Promise<void> => {
    this.assert();
    if ((format !== 'all' && !passthroughFormats.includes(format)) || (value !== 'auto' && value !== 'off')) throw new Error('native_device_setting_value_invalid');
    await this.ports.storage.change('preferences:v1', raw => {
      this.assert();
      if (raw != null && !object(raw)) throw new Error('native_device_settings_record_invalid');
      const existing = object(raw) ? raw : {}, formats = {...(object(existing.passthroughFormats) ? existing.passthroughFormats : {})};
      if (format !== 'all' && existing.disablePassthrough === true) for (const key of passthroughFormats) formats[key] = 'off';
      for (const key of format === 'all' ? passthroughFormats : [format]) formats[key] = value;
      return {record: {...existing, passthroughFormats: formats, disablePassthrough: format === 'all' && value === 'off'}, value: undefined};
    });
    const observed = await this.ports.storage.change('preferences:v1', raw => {this.assert(); return {record: raw, value: raw};});
    this.assert(); this.ports.acceptRecord(observed);
  };
  subscribe = (listener: () => void): (() => void) => {
    this.assert(); return this.owner.subscribePreferences(() => {if (this.owner.current()) listener();});
  };
  write = async (id: CompositeId | 'lossNotices' | 'matchFrameRate' | 'matchDynamicRange' | 'allowInsecure', value: string | boolean): Promise<void> => {
    this.assert();
    if (!nativeDeviceSettingSupported(id)) throw Object.assign(new Error('native_device_setting_unsupported'), {code: 'unsupported'});
    if (id === 'playerEngine' ? !engineSettings.some(setting => setting === value)
      : id === 'matchFrameRate' || id === 'matchDynamicRange' ? value !== 'auto' && value !== 'off'
      : id === 'allowInsecure' ? !insecureChoicesFor(nativeConnectionPolicy().platform).some(choice => choice === value)
      : id === 'music.sampleRateMatching' ? typeof value !== 'string' || !['smart', 'strict', 'off'].includes(value)
      : typeof value !== 'boolean') throw new Error('native_device_setting_value_invalid');
    await this.ports.storage.change('preferences:v1', raw => {
      this.assert();
      if (raw !== null && raw !== undefined && !object(raw)) throw new Error('native_device_settings_record_invalid');
      return {record: {...(object(raw) ? raw : {}), [id]: value}, value: undefined};
    });
    const observed = await this.ports.storage.change('preferences:v1', raw => {this.assert(); return {record: raw, value: raw};});
    this.assert(); this.ports.acceptRecord(observed);
    if (id === 'allowInsecure') acceptConnectionPreference(object(observed) ? observed.allowInsecure : undefined);
  };
}

/** The bootstrap binds this once to its existing record/cache and playback
 * subscription. Borrowers never create a store, revision or optimistic update. */
export function bindNativeDeviceSettings(owner: Owner, ports: Ports): NativeDeviceSettings {
  if (!owner.current()) throw ended();
  if (sources.has(owner)) throw new Error('native_device_settings_already_bound');
  const source = new NativeDeviceSettings(owner, ports); sources.set(owner, source); return source;
}
export function nativeDeviceSettings(owner: Owner): NativeDeviceSettings {
  if (!owner.current()) throw ended();
  const source = sources.get(owner);
  if (!source) throw Object.assign(new Error('native_device_settings_unavailable'), {code: 'unsupported'});
  return source;
}
