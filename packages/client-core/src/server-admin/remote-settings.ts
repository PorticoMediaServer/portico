import type {HttpLocalApi} from '../index.ts';
import type {RemoteAccessStatus, NetworkSettings, BlockedNetwork} from '../../../contracts/src/next/api.ts';
import {callServer, serverPath} from '../query/server-api.ts';
import {putSettingsDocument, settingsSaveConflict} from '../settings-document.ts';
import {readNetworkInterface, saveNetworkInterface, type NetworkInterfaceSettings} from './network-interface.ts';
import {settingsBinding, type SettingsBindingValue} from '../presentation/settings-bindings.ts';
import {settingsRowIndex} from '../presentation/settings-spec.ts';
import type {ConfigurationValue, SettingsConfiguration} from '../settings-configuration.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {throwIfAborted} from '../abort-lifetime.ts';

export type OwnerRemoteStatus = RemoteAccessStatus & {
 outside?: {checkedAt?: string; addresses?: {address: string; port: number; reachable: boolean}[]; problem?: string};
 managedCertificate?: {state: string; ready: boolean; name?: string; expiresAt?: string; nextTryAt?: string; problem?: string};
 listener?: {address: string; port: number};
};
type Name = 'remote-access' | 'network';
type Confirmation = Readonly<{name: Name; etag: string; body: Record<string, unknown>; message: string; code: string}>;
export type OwnerRemoteSnapshot = Readonly<{status?: OwnerRemoteStatus; interfaces?: NetworkInterfaceSettings; blocks: readonly BlockedNetwork[]; busy: boolean; checking?: boolean; error?: unknown; confirmation?: Confirmation}>;
const rows: Readonly<Record<string, readonly [Name, string]>> = {
 'remote.access': ['remote-access', 'remoteAccess'], 'remote.security': ['remote-access', 'remoteSecurity'],
 'remote.secureConnections': ['remote-access', 'secureConnections'], 'remote.signInOutside': ['remote-access', 'signInFromOutside'],
 'remote.mapping': ['remote-access', 'portMapping'], 'remote.publicPort': ['remote-access', 'publicPort'],
 'remote.ipv6': ['remote-access', 'ipv6'], 'remote.discovery': ['remote-access', 'findAtHome'], 'remote.dlna': ['remote-access', 'dlna'],
 'remote.ownCertificate': ['remote-access', 'ownAddress'], 'remote.externalAddress': ['remote-access', 'ownAddress'],
 'remote.certFile': ['remote-access', 'ownAddress'], 'remote.certKey': ['remote-access', 'ownAddress'],
 'remote.customAddresses': ['remote-access', 'allowedAddresses'], 'remote.interface': ['remote-access', 'advertisedInterface'],
 'remote.homeNets': ['network', 'homeNetworks'], 'remote.trustedProxies': ['network', 'trustedProxies'],
};
const invalid = (): never => {throw new Error(unreadableServerResponse);};
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const remoteBody = (document: Readonly<Record<string, unknown>>) => {
 const {interfaces, interfaceNote, warning, ipv6Note, ...body} = document;return body;
};
function shown(id: string, value: unknown): unknown {
 if (id === 'remote.mapping') return value === 'automatic' ? 'auto' : value;
 if (id === 'remote.interface') return value || 'all';
 if (id === 'remote.ownCertificate') return object(value) ? value.enabled : undefined;
 if (id === 'remote.externalAddress') return object(value) && typeof value.domain === 'string' ? value.domain ? `https://${value.domain}` : '' : undefined;
 if (id === 'remote.certFile') return object(value) ? value.certificateFile : undefined;
 if (id === 'remote.certKey') return object(value) ? value.keyFile : undefined;
 return value;
}

/** Remote access's actual documents, exact read versions and current owner facts. */
export class OwnerRemoteSettings {
 private state: OwnerRemoteSnapshot = {blocks: [], busy: false};
 private network?: {values: NetworkSettings; etag: string};
 private configuration?: SettingsConfiguration;
 private readonly listeners = new Set<() => void>();
 private readonly life = new AbortController();
 constructor(private readonly api: Pick<HttpLocalApi, 'request' | 'requestRaw'>, private readonly current: () => boolean) {}
 subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
 getSnapshot = () => this.state;
 private publish(patch: Partial<OwnerRemoteSnapshot>) {if (!this.life.signal.aborted && this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
 has = (id: string) => Object.hasOwn(rows, id);
 read = (id: string): SettingsBindingValue | undefined => {
  const field = this.configuration?.fields.find(field => field.id === id);
  return field && {current: field.current, effective: field.effective, default: field.default, source: field.source, configOnly: false};
 };
 readConfiguration = async (caller?: AbortSignal): Promise<SettingsConfiguration> => {
  const signal = AbortSignal.any([this.life.signal, ...(caller ? [caller] : [])]);
  const [interfaces, network, status, blocked] = await Promise.all([
   readNetworkInterface(this.api, signal),
   this.api.requestRaw(serverPath('GET /v1/admin/settings/network'), 'GET', {signal}),
   callServer(this.api, 'GET /v1/admin/remote-access', {signal}),
   callServer(this.api, 'GET /v1/admin/blocked-networks', {signal}),
  ]);
  if (network.status >= 400) {
   const error = object(network.body) && object(network.body.error) ? network.body.error : {};
   throw Object.assign(new Error(String(error.message || unreadableServerResponse)), {status: network.status, code: error.code});
  }
  const etag = network.headers.etag ?? network.headers.ETag;
  if (!/^"[0-9]+"$/.test(etag ?? '') || !object(network.body) || !Array.isArray(network.body.homeNetworks) || !Array.isArray(blocked.items) || !status.thisDevice || !Array.isArray(status.refused)) return invalid();
  const values = network.body as unknown as NetworkSettings;
  const fields: ConfigurationValue[] = [];
  for (const [id, [name, key]] of Object.entries(rows)) {
   const raw = name === 'network' ? (values as unknown as Record<string, unknown>)[key] : interfaces.document[key];
   const current = shown(id, raw);if (current === undefined) return invalid();
   const original = settingsRowIndex.get(id)?.row.control.default;
   fields.push({id, key: settingsBinding(id)?.key ?? key, current, effective: current, default: original,
    source: JSON.stringify(current) === JSON.stringify(original) ? 'default' : 'server', configOnly: false, applicationMode: 'next-operation'});
  }
  throwIfAborted(signal);
  if (!this.current()) throw new DOMException('Remote Settings retired', 'AbortError');
  this.network = {values, etag};
  this.configuration = {revision: Number(interfaces.etag.slice(1, -1)), registryRevision: 'remote-access', path: '', signInPolicyRevision: '', fields};
  this.publish({interfaces, status: {...status, warnings: [...new Map([...status.warnings, ...values.warnings].map(warning => [warning.code, warning])).values()]}, blocks: blocked.items, error: undefined});
  return this.configuration;
 };
 private async write(name: Name, etag: string, body: Record<string, unknown>, signal?: AbortSignal) {
  try {await putSettingsDocument(this.api, name, etag, body, signal);this.publish({confirmation: undefined});}
  catch (error) {
   const code = (error as {code?: string}).code;
   if (code === 'would_refuse_this_route' || code === 'would_refuse_sign_in') this.publish({confirmation: {name, etag, body, code, message: (error as Error).message}});
   throw error;
  }
 }
 save = async (id: string, value: unknown, signal?: AbortSignal): Promise<void> => {
  if (!this.current() || this.life.signal.aborted) throw new DOMException('Remote Settings retired', 'AbortError');
  const row = rows[id], remote = this.state.interfaces;if (!row || !remote || !this.network || this.state.confirmation) return invalid();
  const [name, key] = row;
  if (id === 'remote.interface') {await saveNetworkInterface(this.api, remote, value === 'all' ? '' : String(value), signal);return;}
  const body: Record<string, unknown> = name === 'remote-access' ? remoteBody(remote.document) : (() => {const {homeNetworksDefault, homeNetworks, builtInWebOrigins, warnings, ...settings} = this.network!.values;return {...settings, ...(!homeNetworksDefault ? {homeNetworks} : {})};})();
  let raw = value;
  if (id === 'remote.access' && value !== 'on' && value !== 'off') return invalid();
  if (id === 'remote.mapping') raw = value === 'auto' ? 'automatic' : value;
  if (['remote.ownCertificate', 'remote.externalAddress', 'remote.certFile', 'remote.certKey'].includes(id)) {
   const address = {...(remote.document.ownAddress as Record<string, unknown>)};
   const field = id === 'remote.ownCertificate' ? 'enabled' : id === 'remote.externalAddress' ? 'domain' : id === 'remote.certFile' ? 'certificateFile' : 'keyFile';
   if (field === 'domain' && value) {
    const url = new URL(String(value).includes('://') ? String(value) : `https://${value}`);
    if (url.protocol !== 'https:' || url.username || url.password || url.port || url.pathname !== '/' || url.search || url.hash) return invalid();
    raw = url.hostname;
   }
   address[field] = raw;body[key] = address;
  } else body[key] = raw;
  await this.write(name, name === 'network' ? this.network.etag : remote.etag, body, signal);
 };
 saveOwnAddress = (ownAddress: {enabled: boolean; domain: string; certificateFile: string; keyFile: string}) => this.command(async () => {
  const remote = this.state.interfaces;if (!remote || this.state.confirmation) return invalid();
  await this.write('remote-access', remote.etag, {...remoteBody(remote.document), ownAddress}, this.life.signal);
 });
 confirm = async () => {
  const pending = this.state.confirmation;if (!pending || this.state.busy || !this.current()) return;
  return this.command(() => this.write(pending.name, pending.etag, {...pending.body, [pending.code === 'would_refuse_sign_in' ? 'confirmOwnerOnly' : 'confirmRefusal']: true}, this.life.signal));
 };
 cancelConfirmation = () => this.publish({confirmation: undefined});
 private async command(run: () => Promise<unknown>) {
  if (this.state.busy || !this.current()) return;
  this.publish({busy: true, error: undefined});
  try {if (!this.current() || this.life.signal.aborted) return false;await run();await this.readConfiguration();return true;}
  catch (error) {if (settingsSaveConflict(error)) {this.publish({confirmation: undefined});await this.readConfiguration().catch(() => {});}this.publish({error});return false;}
  finally {this.publish({busy: false});}
 }
 check = () => this.command(async () => {this.publish({checking: true});try {this.publish({status: await callServer(this.api, 'POST /v1/admin/remote-access/check', {signal: this.life.signal})});} finally {this.publish({checking: false});}});
 unblock = (block: BlockedNetwork) => this.state.blocks.includes(block) ? this.command(() => callServer(this.api, 'DELETE /v1/admin/blocked-networks/{id}', {id: block.id, signal: this.life.signal})) : Promise.resolve();
 allow = (address: string) => this.state.status?.refused.some(row => row.address === address && row.fixes.includes('allow'))
  ? this.command(() => this.save('remote.customAddresses', [...new Set([...(this.state.interfaces?.document.allowedAddresses as string[] ?? []), address])], this.life.signal)) : Promise.resolve();
 dispose = () => {this.life.abort();this.listeners.clear();};
}
