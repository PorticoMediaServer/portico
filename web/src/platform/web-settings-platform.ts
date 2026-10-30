import {installationIdentity} from '@core/installation.ts';
import {browserSettingsEnvironment} from '@core/presentation/settings-visibility.ts';
import type {SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingsDevicePort, SettingsPlatform} from './settings-platform';

const STORAGE_KEY = 'portico.web.device-settings.v1';
/** Device-scope settings a browser can actually own. Everything else stays hidden. */
const DEFAULTS: Readonly<Record<string, SettingsValue>> = Object.freeze({lossNotices: true});

type Record_ = Readonly<{revision: number; values: Readonly<Record<string, SettingsValue>>}>;

function read(): Record_ {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {revision: 1, values: {}};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {revision: 1, values: {}};
    const value = parsed as {revision?: unknown; values?: unknown};
    const revision = typeof value.revision === 'number' && Number.isSafeInteger(value.revision) && value.revision > 0 ? value.revision : 1;
    const values: Record<string, SettingsValue> = {};
    if (value.values && typeof value.values === 'object') for (const [key, entry] of Object.entries(value.values as Record<string, unknown>)) {
      if (key in DEFAULTS && typeof entry === typeof DEFAULTS[key] && (typeof entry === 'boolean' || typeof entry === 'string' || (typeof entry === 'number' && Number.isFinite(entry)))) values[key] = entry as SettingsValue;
    }
    return {revision, values};
  } catch { return {revision: 1, values: {}}; }
}

/**
 * The browser is this device. Its name and app version are the real installation claim the
 * server already binds this session to; a browser cannot rename the system device, so the name
 * stays read-only. The document owns only the drawn device settings a browser can really keep
 * (the picture and sound notices switch); a browser has no offline downloads store or
 * self-updater, so those rows stay absent.
 */
export function webSettingsPlatform(): SettingsPlatform {
  let state = read();
  const listeners = new Set<() => void>();
  const publish = () => { for (const listener of [...listeners]) listener(); };
  const device: SettingsDevicePort = {
    subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    getSnapshot: () => {
      const identity = installationIdentity();
      const facts = identity ? {name: {value: identity.name, readOnly: true}, appVersion: identity.appVersion} : {};
      return {facts, document: {revision: String(state.revision), values: {...DEFAULTS, ...state.values}, defaults: DEFAULTS}};
    },
    apply: async (expectedRevision, patch) => {
      if (String(state.revision) !== expectedRevision) throw new DOMException('Device settings changed', 'AbortError');
      const values: Record<string, SettingsValue> = {...state.values};
      for (const [key, value] of Object.entries(patch)) {
        if (!(key in DEFAULTS)) throw new DOMException('Unknown device setting', 'AbortError');
        if (typeof value !== typeof DEFAULTS[key]) throw new DOMException('Invalid device setting', 'AbortError');
        if (typeof value === 'boolean' || typeof value === 'string' || (typeof value === 'number' && Number.isFinite(value))) values[key] = value;
      }
      state = {revision: state.revision + 1, values};
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify({revision: state.revision, values})); } catch { /* a browser with no storage keeps it for this visit */ }
      publish();
    },
  };
  return {environment: browserSettingsEnvironment, device};
}
