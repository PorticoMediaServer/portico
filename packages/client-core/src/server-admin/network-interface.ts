import type {HttpLocalApi} from '../index.ts';
import {serverPath} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';

export type NetworkInterfaceSettings = Readonly<{
  selected: string;
  interfaces: readonly Readonly<{name: string; addresses: readonly string[]}>[];
  note?: string;
  etag: string;
  /** The remote-access document is replaced whole; preserve every other setting. */
  document: Readonly<Record<string, unknown>>;
}>;
type Api = Pick<HttpLocalApi, 'requestRaw'>;
const path = serverPath('GET /v1/admin/settings/remote-access');
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const invalid = (): never => {throw new Error(unreadableServerResponse);};
function checked(answer: {status: number; body?: unknown}) {
  if (answer.status >= 400) {
    const error = object(answer.body) && object(answer.body.error) ? answer.body.error : {};
    throw Object.assign(new Error(typeof error.message === 'string' ? error.message : unreadableServerResponse), {status: answer.status, code: error.code});
  }
}

export async function readNetworkInterface(api: Api, signal?: AbortSignal): Promise<NetworkInterfaceSettings> {
  const answer = await api.requestRaw(path, 'GET', {signal});
  checked(answer);
  const doc = answer.body, etag = answer.headers.etag ?? answer.headers.ETag;
  if (!object(doc) || typeof doc.advertisedInterface !== 'string' || !/^"[0-9]+"$/.test(etag ?? '') || doc.interfaceNote !== undefined && typeof doc.interfaceNote !== 'string') return invalid();
  const rows = doc.interfaces ?? [];
  if (!Array.isArray(rows)) return invalid();
  const interfaces = rows.map(row => {
    if (!object(row) || typeof row.name !== 'string' || !Array.isArray(row.addresses) || !row.addresses.every(address => typeof address === 'string')) return invalid();
    return Object.freeze({name: row.name, addresses: Object.freeze(row.addresses as string[])});
  });
  return Object.freeze({selected: doc.advertisedInterface, interfaces: Object.freeze(interfaces), etag,
    ...(doc.interfaceNote ? {note: doc.interfaceNote as string} : {}), document: Object.freeze({...doc})});
}

export async function saveNetworkInterface(api: Api, current: NetworkInterfaceSettings, selected: string, signal?: AbortSignal): Promise<NetworkInterfaceSettings> {
  const {interfaces: _interfaces, interfaceNote: _note, ...document} = current.document;
  checked(await api.requestRaw(path, 'PUT', {headers: {'If-Match': current.etag}, body: {...document, advertisedInterface: selected}, signal}));
  return readNetworkInterface(api, signal);
}
