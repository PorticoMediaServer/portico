import {serverNow} from '@core/server-clock.ts';
import {defaultI18n, type MessageId} from '@i18n';
import {codecName} from '@core/presentation/playback-info.ts';
import type {SettingsTableRow} from '../../../kit/settings/SettingsTable';
import type {SettingPresentation} from '../../../kit/settings';
import type {HardwareSnapshot} from './hardware-source';

const t = defaultI18n.t;

/** The stable method tokens the server publishes for one adapter, in the
 * frame's own wording. An unknown token names nothing rather than showing code. */
const methodNames: Readonly<Record<string, MessageId>> = {
  quick_sync: 'next.settings.hardware.method.quick_sync',
  nvenc: 'next.settings.hardware.method.nvenc',
  vaapi: 'next.settings.hardware.method.vaapi',
  amf: 'next.settings.hardware.method.amf',
  videotoolbox: 'next.settings.hardware.method.videotoolbox',
  rkmpp: 'next.settings.hardware.method.rkmpp',
};

/** The server's format names that are not plain codec names (decoder/hardware_facts.go). */
const formatNames: Readonly<Record<string, MessageId>> = {hevc8: 'next.settings.hardware.format.hevc8', hevc10: 'next.settings.hardware.format.hevc10', mpeg2: 'next.settings.hardware.format.mpeg2'};

/** What happened, in one plain sentence, from the probe's stable reason code. */
const reasonNames: Readonly<Record<string, MessageId>> = {
  probe_encode_failed: 'next.settings.hardware.reason.encodeFailed',
  probe_timed_out: 'next.settings.hardware.reason.timedOut',
  confinement_unavailable: 'next.settings.hardware.reason.confinement',
  unsupported_platform: 'next.settings.hardware.reason.unsupported',
  hardware_circuit_open: 'next.settings.hardware.reason.circuitOpen',
  probe_disabled: 'next.settings.hardware.reason.disabled',
};

export function hardwareMethodName(method: string | undefined): string | undefined {
  const name = method && methodNames[method];
  return name ? t(name) : undefined;
}

/** What went wrong in one plain sentence, then the server's own remedy for this kind of install (HW-7);
 * a screen never keeps a second copy of the remedy. */
function faultText(snapshot: HardwareSnapshot): string | undefined {
  const fault = snapshot.hardware?.lastError;
  if (!fault) return undefined;
  const what = fault.reason === 'no_graphics_device' ? undefined : reasonNames[fault.reason] ? t(reasonNames[fault.reason]!) : fault.detail;
  if (fault.remedy) return what ? `${what} ${fault.remedy}` : t('next.settings.hardware.noDevice', {remedy: fault.remedy});
  return what ?? t('next.settings.hardware.noDeviceNoRemedy');
}

/** The drawn grid: each format the server measured, and whether the graphics device or the processor does each job. */
export function hardwareFormats(snapshot: HardwareSnapshot): readonly SettingsTableRow[] | undefined {
  const measured = snapshot.hardware?.formats;
  if (!measured?.length) return undefined;
  const label = (done: boolean) => ({title: done ? t('next.settings.hardware.job.hardware') : t('next.settings.hardware.job.processor'), tone: done ? 'ok' as const : 'quiet' as const});
  return measured.map(row => {const name = formatNames[row.format] ? t(formatNames[row.format]!) : codecName(row.format) ?? row.format;return {id: row.format, label: name, cells: {format: name, decode: label(row.decode), encode: label(row.encode)}};});
}

/** The Hardware rows the server can answer, in the frame's wording. A row with
 * no measured fact is absent rather than drawn as an empty or invented value. */
export function hardwareRowState(rowId: string, snapshot: HardwareSnapshot): SettingPresentation | undefined {
  const hardware = snapshot.hardware;
  if (snapshot.phase !== 'ready' || !hardware) return undefined;
  const devices = hardware.devices.length > 0;
  switch (rowId) {
    case 'streaming.hwError': {
      const text = faultText(snapshot);
      return text ? {present: true, displayValue: text, statusTone: 'warn'} : {present: false};
    }
    case 'streaming.hwDevices':
      return devices ? {present: true} : {present: false};
    case 'streaming.hwFormats':
      return hardwareFormats(snapshot)?.length ? {present: true, gridRows: hardwareFormats(snapshot)} : {present: false};
    case 'streaming.hwToneMapping': {
      // Only a server with a working device tone maps on it; otherwise the processor does.
      if (!devices) return {present: false};
      return hardware.toneMappingWithMasteringData || hardware.toneMappingWithoutMasteringData
        ? {present: true, displayValue: t('next.settings.hardware.tone.hardware'), statusTone: 'ok'}
        : {present: true, displayValue: t('next.settings.hardware.tone.processor'), statusTone: 'quiet'};
    }
    case 'streaming.hwTest': {
      const at = hardware.lastTest ? Date.parse(hardware.lastTest) : 0;
      const when = at > 0 ? defaultI18n.relativeTime(at, serverNow(), {inSentence: true}) : undefined;
      return {present: true, help: when ? t('next.settings.hardware.lastTest', {when}) : t('next.settings.hardware.neverTested'), disabled: snapshot.testing};
    }
    default:
      return undefined;
  }
}

/** The rows this page answers itself, so eligibility asks the same facts the
 * row does instead of looking for a settings binding these rows do not have. */
export const hardwareRowIds: readonly string[] = ['streaming.hwError', 'streaming.hwDevices', 'streaming.hwFormats', 'streaming.hwToneMapping', 'streaming.hwTest'];

export function hardwareEligible(rowId: string, snapshot: HardwareSnapshot): boolean {
  return hardwareRowIds.includes(rowId) && hardwareRowState(rowId, snapshot) !== undefined;
}

/** The one result line a test may leave: good news only, as drawn. */
export function hardwareTestResult(snapshot: HardwareSnapshot): string | undefined {
  return snapshot.tested === 'ok' ? t('next.settings.hardware.testResult.ok')
    : snapshot.tested === 'problem' ? t('next.settings.hardware.testResult.problem') : undefined;
}
