import {defaultI18n, type MessageId} from '@i18n';
import type {SettingsTableRow} from '../../../kit/settings/SettingsTable';
import type {SettingPresentation} from '../../../kit/settings';
import type {HardwareSnapshot} from './hardware-source';
import {transcodeLocationChoice} from '@core/presentation/transcode-location.ts';

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

/** The remedy line is the server's own text for this install (HW-7); a screen
 * never keeps a second copy of it. */
function faultText(snapshot: HardwareSnapshot): string | undefined {
  const fault = snapshot.capacity?.hardware.lastError;
  if (!fault) return undefined;
  const remedy = snapshot.inventory?.platform.remedy;
  const what = fault.reason === 'no_graphics_device' ? undefined : reasonNames[fault.reason] && t(reasonNames[fault.reason]);
  if (remedy) return what ? `${what} ${remedy}` : t('next.settings.hardware.noDevice', {remedy});
  return what ?? t('next.settings.hardware.noDeviceNoRemedy');
}

/** The drawn grid, in the install's published order and with its labels. */
export function hardwareFormats(snapshot: HardwareSnapshot): readonly SettingsTableRow[] | undefined {
  const measured = snapshot.capacity?.hardware.formats;
  const order = snapshot.inventory?.formats;
  if (!measured?.length || !order?.length) return undefined;
  const byFormat = new Map(measured.map(row => [row.format, row]));
  const label = (done: boolean) => ({title: done ? t('next.settings.hardware.job.hardware') : t('next.settings.hardware.job.processor'), tone: done ? 'ok' as const : 'quiet' as const});
  return order.flatMap(format => {
    const row = byFormat.get(format.id);
    if (!row) return [];
    return [{id: format.id, label: format.label, cells: {format: format.label, decode: label(row.decode), encode: label(row.encode)}}];
  });
}

/** The Hardware rows the server can answer, in the frame's wording. A row with
 * no measured fact is absent rather than drawn as an empty or invented value. */
export function hardwareRowState(rowId: string, snapshot: HardwareSnapshot, storedLocation?: string): SettingPresentation | undefined {
  if (snapshot.phase !== 'ready' || !snapshot.capacity) return undefined;
  const hardware = snapshot.capacity.hardware;
  const devices = hardware.devices.length > 0;
  switch (rowId) {
    case 'streaming.transcodeStatus': {
      // The disk fallback/spill folder and whether this
      // server can write there, from the same capacity read the rest of the page
      // uses. A directory with no measured free space says the path only.
      const directory = snapshot.capacity.temporaryDirectory;
      if (!directory.path) return {present: false};
      // A container that never mapped a conversion folder uses its own state
      // directory; the drawn line names the mapping only in that case.
      const advice = directory.fallback && snapshot.inventory?.platform.container === true ? ` ${t('next.settings.streaming.spaceMap')}` : '';
      if (!directory.ready) return {present: true, displayValue: t('next.settings.streaming.spaceNotWritable', {path: directory.path}), statusTone: 'warn'};
      const where = directory.freeBytes === null
        ? t('next.settings.streaming.space', {path: directory.path})
        : t('next.settings.streaming.spaceFree', {path: directory.path, free: defaultI18n.bytes(directory.freeBytes)});
      return {present: true, statusTone: 'ok', displayValue: where + advice};
    }
    case 'streaming.transcodeLocation': {
      // The preference stays selectable while playback safely falls back to disk.
      const {value, memoryAvailable} = transcodeLocationChoice(snapshot.capacity.temporaryDirectory, storedLocation);
      const options = [
        {value: 'memory', label: t('next.settingsSpec.server.streaming.groups.2.rows.1.control.options.0.label')},
        {value: 'disk', label: t('next.settingsSpec.server.streaming.groups.2.rows.1.control.options.1.label')},
      ];
      return {present: true, value, defaultValue: 'memory', choices: options, help: memoryAvailable ? undefined : t('next.settings.streaming.memoryUnavailable')};
    }
    case 'streaming.hwError': {
      const text = faultText(snapshot);
      return text ? {present: true, displayValue: text, statusTone: 'warn'} : {present: false};
    }
    case 'streaming.hwDevices':
      return devices ? {present: true} : {present: false};
    case 'streaming.hwFormats':
      return hardwareFormats(snapshot)?.length ? {present: true, gridRows: hardwareFormats(snapshot)} : {present: false};
    case 'streaming.hwToneMapping': {
      // Only a server with a working adapter tone maps in hardware; a software
      // server says so, and an owner who turned it off reads Off.
      if (!devices) return {present: false};
      const status = snapshot.capacity.hdrToneMapping.status;
      if (status === 'disabled') return {present: true, displayValue: t('next.settings.hardware.tone.off'), statusTone: 'quiet'};
      if (status === 'available') return {present: true, displayValue: t('next.settings.hardware.tone.hardware'), statusTone: 'ok'};
      if (status === 'limited') return {present: true, displayValue: t('next.settings.hardware.tone.processor'), statusTone: 'quiet'};
      return {present: false};
    }
    case 'streaming.hwTest': {
      const at = hardware.lastTest * 1000;
      const when = at > 0 ? defaultI18n.relativeTime(at, Date.now()) : undefined;
      return {present: true, help: when ? t('next.settings.hardware.lastTest', {when}) : t('next.settings.hardware.neverTested'), disabled: snapshot.testing};
    }
    default:
      return undefined;
  }
}

/** The rows this page answers itself, so eligibility asks the same facts the
 * row does instead of looking for a settings binding these rows do not have. */
export const hardwareRowIds: readonly string[] = ['streaming.transcodeStatus', 'streaming.transcodeLocation', 'streaming.hwError', 'streaming.hwDevices', 'streaming.hwLimit', 'streaming.hwFormats', 'streaming.hwToneMapping', 'streaming.hwTest'];

export function hardwareEligible(rowId: string, snapshot: HardwareSnapshot, storedLocation?: string): boolean {
  return hardwareRowIds.includes(rowId) && hardwareRowState(rowId, snapshot, storedLocation) !== undefined;
}

/** The one result line a test may leave: good news only, as drawn. */
export function hardwareTestResult(snapshot: HardwareSnapshot): string | undefined {
  return snapshot.tested === 'ok' ? t('next.settings.hardware.testResult.ok')
    : snapshot.tested === 'problem' ? t('next.settings.hardware.testResult.problem') : undefined;
}
