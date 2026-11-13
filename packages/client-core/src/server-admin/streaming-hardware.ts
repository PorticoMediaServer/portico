import type {StreamingHardwareState} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';

/** The rebuilt server's measured facts; no legacy capacity/session/tool placeholders. */
export type OwnerStreamingHardware = StreamingHardwareState;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === 'string';
function parse(value: unknown): OwnerStreamingHardware {
  const fail = (): never => {throw new Error(unreadableServerResponse);};
  if (!object(value) || typeof value.checking !== 'boolean' || !Array.isArray(value.devices) || !Array.isArray(value.formats)) return fail();
  const tone = (v: Record<string, unknown>) => typeof v.toneMappingWithMasteringData === 'boolean' && typeof v.toneMappingWithoutMasteringData === 'boolean';
  const formats = (v: unknown) => Array.isArray(v) && v.every(f => object(f) && text(f.format) && typeof f.decode === 'boolean' && typeof f.encode === 'boolean');
  if (!tone(value) || !formats(value.formats)) return fail();
  if (!value.devices.every(d => object(d) && text(d.id) && text(d.name) && typeof d.inUse === 'boolean' && tone(d) && formats(d.formats)
    && (d.method === undefined || text(d.method)) && (d.problem === undefined || text(d.problem)))) return fail();
  if (value.lastTest !== undefined && (!text(value.lastTest) || !Number.isFinite(Date.parse(value.lastTest)))) return fail();
  if (value.lastError !== undefined && (!object(value.lastError) || !text(value.lastError.reason)
    || value.lastError.detail !== undefined && !text(value.lastError.detail) || value.lastError.remedy !== undefined && !text(value.lastError.remedy))) return fail();
  return value as OwnerStreamingHardware;
}
export async function readOwnerStreamingHardware(api: ServerRequester, signal?: AbortSignal): Promise<OwnerStreamingHardware> {
  return parse(await callServer(api, 'GET /v1/admin/hardware', {signal}));
}
/** Starts the shared bounded test; checking remains true until a later read sees its result. */
export async function testOwnerStreamingHardware(api: ServerRequester, signal?: AbortSignal): Promise<OwnerStreamingHardware> {
  return parse(await callServer(api, 'POST /v1/admin/hardware/test', {signal}));
}
