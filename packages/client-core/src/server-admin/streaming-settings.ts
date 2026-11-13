import {putSettingsDocument} from '../settings-document.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import type {HttpLocalApi} from '../index.ts';
import {callServer} from '../query/server-api.ts';
import type {ConfigurationValue, SettingsConfiguration} from '../settings-configuration.ts';
import {settingsBinding, settingsBindingPatch} from '../presentation/settings-bindings.ts';
import {unreadableServerResponse} from '../server-messages.ts';

type DocumentName = 'streaming' | 'downloads';
const rows: Readonly<Record<string, readonly [DocumentName | 'limits', string]>> = {
  'streaming.allowConversion': ['streaming', 'convertWhenNeeded'],
  'streaming.awayExact': ['streaming', 'awayVideoKbps'],
  'streaming.musicAwayQuality': ['streaming', 'awayMusicKbps'],
  'streaming.uploadSpeed': ['streaming', 'uploadSpeedKbps'],
  'streaming.hardware': ['streaming', 'graphicsDevice'],
  'streaming.hardwareDecode': ['streaming', 'hardwareDecoding'],
  'streaming.maxConversions': ['streaming', 'conversionsAtOnce'],
  'streaming.maxSoftwareConversions': ['streaming', 'processorConversionsAtOnce'],
  'streaming.maxBackgroundConversions': ['streaming', 'backgroundConversionsAtOnce'],
  'streaming.toneMapping': ['streaming', 'toneMapping'],
  'streaming.transcodeFolder': ['streaming', 'conversionFolder'],
  'streaming.ffmpegPath': ['streaming', 'ffmpegProgram'],
  'streaming.downloadQuality': ['downloads', 'highestQuality'],
  'streaming.maxStreams': ['limits', 'total'],
  'streaming.pausedTimeout': ['limits', 'endPausedVideoAfterMs'],
};
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const invalid = (): never => {throw new Error(unreadableServerResponse);};
const keyFor = (id: string) => id === 'streaming.awayQuality' ? 'streaming.awayExact' : id;

/** Values in the existing row binding's vocabulary; no unsupported setting is published. */
function shown(id: string, value: unknown): unknown {
  switch (id) {
    case 'streaming.allowConversion': return value === 'picture-and-sound' ? 'all' : value;
    case 'streaming.hardware': return value === 'automatic' ? 'auto' : value === 'processor' ? 'software' : value;
    case 'streaming.awayExact': return typeof value === 'number' ? value / 1000 : invalid();
    case 'streaming.uploadSpeed': return value === 0 ? null : typeof value === 'number' ? value / 1000 : invalid();
    case 'streaming.pausedTimeout': return typeof value === 'number' ? value / 60000 : invalid();
    case 'streaming.musicAwayQuality': return value === 0 ? 'original' : value === 1411 ? 'cd-quality' : String(value);
    default: return value;
  }
}

/** One visit's real documents and their read ETags, used by the existing settings controller. */
export class OwnerStreamingSettings {
  private readonly etags = new Map<DocumentName, string>();
  private configuration?: SettingsConfiguration;
  private conversionProblem?: string;
  constructor(private readonly api: Pick<HttpLocalApi, 'request'> & Partial<Pick<HttpLocalApi, 'requestRaw'>>) {}
  has = (id: string): boolean => Object.hasOwn(rows, keyFor(id));
  problemFor = (id: string): string | undefined => ['streaming.ffmpegPath', 'streaming.transcodeFolder'].includes(id) ? this.conversionProblem : undefined;
  readConfiguration = async (signal?: AbortSignal): Promise<SettingsConfiguration> => {
    const [streaming, downloads, limits] = await Promise.all([
      callServer(this.api, 'GET /v1/admin/settings/{name}', {id: 'streaming', signal}),
      callServer(this.api, 'GET /v1/admin/settings/{name}', {id: 'downloads', signal}),
      callServer(this.api, 'GET /v1/admin/playback/stream-limits', {signal}),
    ]);
    const documents = {streaming, downloads};
    for (const name of ['streaming', 'downloads'] as const) {
      const doc = documents[name];
      if (doc.name !== name || !/^"[0-9]+"$/.test(doc.etag) || !object(doc.values) || !object(doc.defaults)) invalid();
    }
    const fields: ConfigurationValue[] = [];
    for (const [id, [name, key]] of Object.entries(rows)) {
      const doc = name === 'limits' ? undefined : documents[name];
      const values = doc ? doc.values : limits, defaults = doc ? doc.defaults : {total: 0, endPausedVideoAfterMs: 0};
      if (!object(values) || !object(defaults)) return invalid();
      if (doc && !Object.hasOwn(values, key) && !Object.hasOwn(defaults, key)) continue;
      const current = shown(id, values[key] ?? (name === 'limits' ? 0 : invalid()));
      const original = shown(id, defaults[key]);
      if (current === undefined || original === undefined) invalid();
      fields.push(Object.freeze({id, key: settingsBinding(id)!.key, current, effective: current, default: original,
        source: current === original ? 'default' : 'server', configOnly: false, applicationMode: 'next-operation'}));
    }
    throwIfAborted(signal);
    this.conversionProblem = streaming.problem || undefined;
    for (const name of ['streaming', 'downloads'] as const) this.etags.set(name, documents[name].etag);
    return this.configuration = Object.freeze({revision: Number(streaming.etag.slice(1, -1)), registryRevision: 'streaming',
      path: '', signInPolicyRevision: '', fields: Object.freeze(fields)});
  };
  save = async (id: string, value: unknown, signal?: AbortSignal): Promise<void> => {
    const row = rows[keyFor(id)], binding = settingsBinding(id);
    if (!row || !binding || !this.configuration) return invalid();
    const [name, key] = row;
    let raw = settingsBindingPatch(binding, value, {configuration: this.configuration}).values[binding.key];
    if (key === 'convertWhenNeeded' && raw === 'all') raw = 'picture-and-sound';
    if (key === 'graphicsDevice') raw = raw === 'auto' ? 'automatic' : raw === 'software' ? 'processor' : raw;
    if (key === 'awayMusicKbps') raw = raw === 'original' ? 0 : raw === 'cd-quality' ? 1411 : Number(raw);
    if (key === 'uploadSpeedKbps' || key === 'total') raw ??= 0;
    if (key === 'endPausedVideoAfterMs') raw = Number(raw) * 60000;
    if (name === 'limits') {
      await callServer(this.api, 'PATCH /v1/admin/playback/stream-limits', {body: {[key]: raw}, signal});
      return;
    }
    const etag = this.etags.get(name); if (!etag) return invalid();
    await putSettingsDocument(this.api, name, etag, {[key]: raw}, signal);
  };
}
