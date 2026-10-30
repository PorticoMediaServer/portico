import {defaultI18n, type MessageId} from '@i18n';
import type {SourceChoiceDefinition, SourceChoiceField} from '@core/remote-source-choice.ts';

const t = defaultI18n.t;
const GB = 1024 * 1024 * 1024;

/** The exact option labels SourceAdvancedRows draws for the check interval. */
const intervalLabels: Readonly<Record<string, MessageId>> = {
  '900': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.0.label',
  '3600': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.1.label',
  '21600': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.2.label',
  '86400': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.3.label',
  '604800': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.4.label',
  '0': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.0.control.options.5.label',
};
/** The exact full re-check labels SourceAdvancedRows draws. */
const recheckLabels: Readonly<Record<string, MessageId>> = {
  '7': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.0.label',
  '14': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.1.label',
  '30': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.2.label',
  '0': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.0.rows.3.control.options.3.label',
};
/** The exact intro-mode labels SourceAdvancedRows draws. */
const introsLabels: Readonly<Record<string, MessageId>> = {
  off: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.0.label',
  inherit: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.1.label',
  sound: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.2.label',
  picture: 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.3.control.options.3.label',
};
/** The exact read-cache sizes, in total bytes, against their drawn option labels. */
const cacheLabels: Readonly<Record<string, MessageId>> = {
  '0': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.0.label',
  '2': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.1.label',
  '4': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.2.label',
  '8': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.3.label',
  '16': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.4.label',
  '32': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.5.label',
  '64': 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.1.control.options.6.label',
};
/** The exact free-disk floors, in gigabytes, against their drawn option labels. */
const floorLabels: Readonly<Record<string, MessageId>> = {
  '1': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.0.label',
  '2': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.1.label',
  '4': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.2.label',
  '8': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.3.label',
  '16': 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.2.rows.0.control.options.4.label',
};

const runWord: MessageId = 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.0.label';
const skipWord: MessageId = 'next.settingsSpec.server.remote-sources.panels.source.tabs.3.groups.1.rows.2.control.columns.1.label';

/** The drawn words for one chosen value, from the exact canonical definition and
 * nothing else. It reads no comparison, state, server row, clock or IO, and it
 * returns undefined whenever the value is not one the drawn control offers, rather
 * than rounding or inventing an option. */
export function sourceChoiceValue(field: SourceChoiceField, definition: SourceChoiceDefinition): string | undefined {
  const {reading, advanced} = definition;
  switch (field) {
    case 'connections': return `${reading.connections} ${t('next.remoteSources.atOnce')}`;
    case 'requestsPerSecond': return `${reading.requestsPerSecond} ${t('next.remoteSources.aSecond')}`;
    case 'bytesPerSecond': return reading.bytesPerSecond > 0 ? `${reading.bytesPerSecond / 1_000_000} ${t('next.remoteSources.megabytesPerSecond')}` : t('next.remoteSources.noLimit');
    case 'scanIntervalSeconds': {const label = intervalLabels[String(advanced.scanIntervalSeconds)];return label ? t(label) : undefined;}
    case 'fullRecheckDays': {const label = recheckLabels[String(advanced.fullRecheckDays)];return label ? t(label) : undefined;}
    case 'introsMode': {const label = introsLabels[advanced.introsMode];return label ? t(label) : undefined;}
    case 'cacheBytes': return gigabyteOption(advanced.cacheBytes, cacheLabels);
    case 'cacheFloorBytes': return gigabyteOption(advanced.cacheFloorBytes, floorLabels);
    case 'watchForChanges': return advanced.watchForChanges ? t('next.remoteSources.on') : t('next.controls.off');
    case 'scanOnStart': return advanced.scanOnStart ? t('next.remoteSources.on') : t('next.controls.off');
    case 'doubleCheckFolders': return advanced.doubleCheckFolders ? t('next.remoteSources.on') : t('next.controls.off');
    case 'seekPreviews': return t(advanced.seekPreviews ? runWord : skipWord);
    case 'chapterImages': return t(advanced.chapterImages ? runWord : skipWord);
    case 'waveforms': return t(advanced.waveforms ? runWord : skipWord);
    case 'loudness': return t(advanced.loudness ? runWord : skipWord);
    case 'checksums': return t(advanced.checksums ? runWord : skipWord);
    case 'dirCacheSeconds': return advanced.dirCacheSeconds % 60 === 0 ? `${advanced.dirCacheSeconds / 60} ${t('next.remoteSources.advMinutesUnit')}` : undefined;
    case 'readBufferBytes': return advanced.readBufferBytes % (1024 * 1024) === 0 ? `${advanced.readBufferBytes / (1024 * 1024)} ${t('next.remoteSources.advMegabytesUnit')}` : undefined;
    case 'connectTimeoutSeconds': return `${Math.round(advanced.connectTimeoutSeconds)} ${t('next.remoteSources.advSecondsUnit')}`;
    case 'idleTimeoutSeconds': return `${Math.round(advanced.idleTimeoutSeconds)} ${t('next.remoteSources.advSecondsUnit')}`;
    case 'retries': return `${Math.round(advanced.retries)} ${t('next.remoteSources.advTimesUnit')}`;
    case 'lowLevelRetries': return `${Math.round(advanced.lowLevelRetries)} ${t('next.remoteSources.advTimesUnit')}`;
    default: return undefined;
  }
}

/** A cache figure names a drawn option only at its exact size; a fraction of a
 * gigabyte has no drawn label and is not rounded into one. */
function gigabyteOption(bytes: number, labels: Readonly<Record<string, MessageId>>): string | undefined {
  if (!Number.isInteger(bytes) || bytes % GB !== 0) return undefined;
  const label = labels[String(bytes / GB)];
  return label ? t(label) : undefined;
}
