import type {StreamingHardwareState} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {codecName} from '../presentation/playback-info.ts';
import {defaultI18n, type MessageId, type MessageValues} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;

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
  return value as unknown as OwnerStreamingHardware;
}
export async function readOwnerStreamingHardware(api: ServerRequester, signal?: AbortSignal): Promise<OwnerStreamingHardware> {
  return parse(await callServer(api, 'GET /v1/admin/hardware', {signal}));
}
/** Starts the shared bounded test; checking remains true until a later read sees its result. */
export async function testOwnerStreamingHardware(api: ServerRequester, signal?: AbortSignal): Promise<OwnerStreamingHardware> {
  return parse(await callServer(api, 'POST /v1/admin/hardware/test', {signal}));
}

/** Test hardware: the server starts one bounded test and says `checking` until it has finished, so the report
 * is read again every two seconds until then (at most 90 seconds). It never needs a restart (rule 34). */
export async function runOwnerHardwareTest(api: ServerRequester, wait: (ms: number) => Promise<void>, signal?: AbortSignal): Promise<OwnerStreamingHardware> {
  let hardware = await testOwnerStreamingHardware(api, signal);
  for (let n = 0; hardware.checking && n < 45 && !signal?.aborted; n++) {
    await wait(2000);
    hardware = await readOwnerStreamingHardware(api, signal);
  }
  return hardware;
}

// ── The words a Hardware group draws, shared by every app ─────────────────────────────────────

/** The stable method tokens the server publishes for one adapter; an unknown token names nothing rather than showing code. */
const methodNames: Readonly<Record<string, MessageId>> = {
  quick_sync: 'next.settings.hardware.method.quick_sync', nvenc: 'next.settings.hardware.method.nvenc', vaapi: 'next.settings.hardware.method.vaapi',
  amf: 'next.settings.hardware.method.amf', videotoolbox: 'next.settings.hardware.method.videotoolbox', rkmpp: 'next.settings.hardware.method.rkmpp',
};
/** The server's format names that are not plain codec names (decoder/hardware_facts.go). */
const formatNames: Readonly<Record<string, MessageId>> = {hevc8: 'next.settings.hardware.format.hevc8', hevc10: 'next.settings.hardware.format.hevc10', mpeg2: 'next.settings.hardware.format.mpeg2'};
/** What happened, in one plain sentence, from the probe's stable reason code. */
const reasonNames: Readonly<Record<string, MessageId>> = {
  probe_encode_failed: 'next.settings.hardware.reason.encodeFailed', probe_timed_out: 'next.settings.hardware.reason.timedOut',
  confinement_unavailable: 'next.settings.hardware.reason.confinement', unsupported_platform: 'next.settings.hardware.reason.unsupported',
  hardware_circuit_open: 'next.settings.hardware.reason.circuitOpen', probe_disabled: 'next.settings.hardware.reason.disabled',
};
export function hardwareMethodName(method: string | undefined, t: Translate = defaultI18n.t): string | undefined {
  const name = method && methodNames[method];
  return name ? t(name) : undefined;
}
export function hardwareFormatName(format: string, t: Translate = defaultI18n.t): string {
  return formatNames[format] ? t(formatNames[format]!) : codecName(format) ?? format;
}
/** What went wrong in one plain sentence, then the server's own remedy for this kind of install (HW-7). */
export function hardwareFaultText(hardware: OwnerStreamingHardware, t: Translate = defaultI18n.t): string | undefined {
  const fault = hardware.lastError;
  if (!fault) return undefined;
  const what = fault.reason === 'no_graphics_device' ? undefined : reasonNames[fault.reason] ? t(reasonNames[fault.reason]!) : fault.detail;
  if (fault.remedy) return what ? `${what} ${fault.remedy}` : t('next.settings.hardware.noDevice', {remedy: fault.remedy});
  return what ?? t('next.settings.hardware.noDeviceNoRemedy');
}
/** Only a server with a working device tone maps on it; otherwise the processor does. Undefined with no device. */
export function hardwareToneMapping(hardware: OwnerStreamingHardware): 'hardware' | 'processor' | undefined {
  if (!hardware.devices.length) return undefined;
  return hardware.toneMappingWithMasteringData || hardware.toneMappingWithoutMasteringData ? 'hardware' : 'processor';
}
