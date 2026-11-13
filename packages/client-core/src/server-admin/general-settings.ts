import type {SettingsDocumentAPI} from '../settings-document.ts';
import type {HttpLocalApi} from '../index.ts';
import {readNetworkInterface} from './network-interface.ts';
import {putSettingsDocument} from '../settings-document.ts';
import {callServer} from '../query/server-api.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import type {SettingsConfiguration, ConfigurationValue} from '../settings-configuration.ts';
import type {SettingsBindingValue} from '../presentation/settings-bindings.ts';
import {settingsBinding} from '../presentation/settings-bindings.ts';
import {unreadableServerResponse} from '../server-messages.ts';

type Name = 'general' | 'storage' | 'remote-access' | 'notices';
type Document = {etag: string; values: Record<string, any>; defaults: Record<string, any>};
const names: readonly Name[] = ['general', 'storage', 'remote-access', 'notices'];
const rows: Readonly<Record<string, readonly [Name, string]>> = {
 'general.name': ['general', 'name'], 'general.backgroundSpeed': ['general', 'backgroundSpeed'],
 'general.updateChecks': ['general', 'updateCheck'], 'general.updateFeed': ['general', 'updateAddress'],
 'general.heavyWork': ['general', 'heavyWork'], 'general.heavyStart': ['general', 'heavyWork'],
 'general.heavyLength': ['general', 'heavyWork'], 'general.heavyDays': ['general', 'heavyWork'],
 'general.alertStorageWarn': ['storage', 'warnPercent'], 'general.alertStorageCritical': ['storage', 'majorPercent'],
 'general.alertCertificateDays': ['remote-access', 'certificateWarningDays'], 'general.serverNoticesTo': ['notices', 'serverNoticesTo'],
};
const days = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday'] as const;
const minute = (time: string) => Number(time.slice(0, 2)) * 60 + Number(time.slice(3));
const clock = (minutes: number) => `${String(Math.floor(minutes / 60) % 24).padStart(2, '0')}:${String(minutes % 60).padStart(2, '0')}`;
const invalid = (): never => {throw new Error(unreadableServerResponse);};
function shown(id: string, v: any): unknown {
 if (id.startsWith('general.heavy') && (!v || !['any', 'overnight', 'chosen'].includes(v.hours))) return invalid();
 if (id === 'general.serverNoticesTo') return v === 'owner' || v === 'owner-and-admins' ? v : invalid();
 if (id === 'general.updateChecks') return v === 'automatic' ? 'auto' : v === 'address' ? 'custom' : v;
 if (id === 'general.updateFeed') return v ?? '';
 if (id === 'general.heavyWork') return v?.hours === 'chosen' ? 'custom' : v?.hours === 'overnight' ? 'overnight' : 'any';
 if (id === 'general.heavyDays') return Object.fromEntries(days.map(d => [d, v?.hours !== 'chosen' || v.days?.includes(d.slice(0, 3)) ? 'runs' : 'skips']));
 if (id === 'general.heavyStart') return String(v?.hours === 'chosen' ? minute(v.from) : 60);
 if (id === 'general.heavyLength') return String(v?.hours === 'chosen' ? ((minute(v.to) - minute(v.from) + 1440) % 1440 || 1440) / 60 : 5);
 if (id.startsWith('general.alert')) return String(v);
 return v;
}
/** Web General uses the real per-area documents; a narrow save carries the ETag it read. */
export class OwnerGeneralSettings {
 private customUpdates = false;
 private documents?: Record<Name, Document>;
 private configuration?: SettingsConfiguration;
 constructor(private readonly api: Pick<HttpLocalApi, 'request' | 'requestRaw'>) {}
 has = (id: string) => Object.hasOwn(rows, id);
 read = (id: string): SettingsBindingValue | undefined => {
  const field = this.configuration?.fields.find(field => field.id === id);
  return field && {current: id === 'general.updateChecks' && this.customUpdates ? 'custom' : field.current, effective: field.effective, default: field.default, source: field.source, configOnly: false};
 };
 readConfiguration = async (signal?: AbortSignal): Promise<SettingsConfiguration> => {
  const documents = {} as Record<Name, Document>;
  await Promise.all(names.map(async name => {
   if (name === 'remote-access') {
    const remote = await readNetworkInterface(this.api, signal);
    documents[name] = {etag: remote.etag, values: {...remote.document}, defaults: {certificateWarningDays: 14}};
    return;
   }
   const doc = await callServer(this.api, 'GET /v1/admin/settings/{name}', {id: name, signal});
   if (doc.name !== name || !/^"[0-9]+"$/.test(doc.etag) || !doc.values || !doc.defaults) invalid();
   documents[name] = doc as Document;
  }));
  const fields: ConfigurationValue[] = Object.entries(rows).map(([id, [name, key]]) => {
   const doc = documents[name], current = shown(id, doc.values[key]), original = shown(id, doc.defaults[key]);
   if (current === undefined || original === undefined) invalid();
   return {id, key: settingsBinding(id)!.key, current, effective: current, default: original,
    source: JSON.stringify(current) === JSON.stringify(original) ? 'default' : 'server', configOnly: false, applicationMode: 'next-operation'};
  });
  throwIfAborted(signal); this.documents = documents;
  return this.configuration = {revision: Number(documents.general.etag.slice(1, -1)), registryRevision: 'general', path: '', signInPolicyRevision: '', fields};
 };
 save = async (id: string, value: unknown, signal?: AbortSignal): Promise<void> => {
  const row = rows[id]; if (!row || !this.documents) return invalid();
  if (id === 'general.serverNoticesTo' && value !== 'owner' && value !== 'owner-and-admins') return invalid();
  const [name, key] = row, doc = this.documents[name]; let change: Record<string, unknown> = {[key]: value};
  if (id === 'general.updateChecks') {
   if (value === 'custom') {this.customUpdates = true; return;}
   if (value !== 'auto' && value !== 'never') return invalid();
   this.customUpdates = false;
   change = {updateCheck: value === 'auto' ? 'automatic' : 'never'};
  } else if (id === 'general.updateFeed') {this.customUpdates = false;change = {updateCheck: value === '' ? 'automatic' : 'address', updateAddress: value};}
  else if (id.startsWith('general.heavy')) {
   const previous = doc.values.heavyWork;
   const hours = previous.hours === 'chosen' ? {...previous} : {hours: 'chosen', days: days.map(d => d.slice(0, 3)), from: '01:00', to: '06:00'};
   if (id === 'general.heavyWork') {
    if (!['any', 'overnight', 'custom'].includes(String(value))) return invalid();
    change = {heavyWork: value === 'custom' ? hours : {hours: value}};
   } else {
    const from = minute(hours.from), length = (minute(hours.to) - from + 1440) % 1440 || 1440;
    if (id === 'general.heavyDays') {
     if (!value || typeof value !== 'object') return invalid();
     hours.days = days.filter(d => (value as Record<string, unknown>)[d] === 'runs').map(d => d.slice(0, 3));
    } else if (id === 'general.heavyStart') {hours.from = clock(Number(value)); hours.to = clock(Number(value) + length);}
    else if (id === 'general.heavyLength') hours.to = clock(from + Number(value) * 60);
    change = {heavyWork: hours};
   }
  } else if (id.startsWith('general.alert')) change = {[key]: Number(value)};
  if (name === 'remote-access') {
   const {interfaces, interfaceNote, warning, ipv6Note, ...settings} = doc.values;
   change = {...settings, ...change};
  }
  await putSettingsDocument(this.api, name, doc.etag, change, signal);
 };
}

export async function readBackgroundPause(api: SettingsDocumentAPI, signal?: AbortSignal): Promise<string | undefined> {
 return (await callServer(api, 'GET /v1/admin/background/pause', {signal})).pausedUntil;
}
export async function setBackgroundPause(api: SettingsDocumentAPI, paused: boolean, signal?: AbortSignal): Promise<string | undefined> {
 return (paused ? await callServer(api, 'PUT /v1/admin/background/pause', {signal}) : await callServer(api, 'DELETE /v1/admin/background/pause', {signal})).pausedUntil;
}
