import {NativeModules, Platform} from 'react-native';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import type {NativeSubtitlePickedFile as PickedFile} from './subtitle-file-upload';

type PickerBridge = {pick: (requestId: string) => Promise<unknown>; cancel: (requestId: string) => Promise<unknown>; discard: (uris: readonly string[]) => Promise<unknown>};
const bridge = NativeModules.PorticoSubtitleFiles as Partial<PickerBridge> | undefined;
const ids = NativeModules.PorticoRuntime as {newRequestId?: () => Promise<unknown>} | undefined;
const failure = () => new Error(serviceText('player.subtitles.changeFailed'));
const formats = ['srt', 'vtt', 'ass', 'ssa', 'sup', 'idx'] as const;
function fileUri(value: unknown): value is string {
  if (typeof value !== 'string' || !value.startsWith('file:///') || /[\u0000-\u0020\u007f?#]/.test(value)) return false;
  try { const url = new URL(value); decodeURIComponent(url.pathname); return url.protocol === 'file:' && !url.host && !url.search && !url.hash && url.pathname.length > 1; } catch { return false; }
}
async function discard(uris: readonly string[]): Promise<void> {
  try { await bridge?.discard?.(uris); } catch {}
}
async function discardResult(value: unknown): Promise<void> {
  if (!value || typeof value !== 'object') return;
  const row = value as Record<string, unknown>;
  await discard([row.fileUri, row.companionUri].filter(fileUri));
}

function selected(value: unknown): PickedFile {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw failure();
  const row = value as Record<string, unknown>;
  if (Object.keys(row).some(key => !['fileUri', 'format', 'title', 'companionUri'].includes(key)) || !formats.includes(row.format as PickedFile['format']) || typeof row.title !== 'string' || !row.title.length || row.title.length > 160) throw failure();
  const format = row.format as PickedFile['format'];
  if (!fileUri(row.fileUri)) throw failure();
  if (format === 'idx' ? !fileUri(row.companionUri) : Object.hasOwn(row, 'companionUri')) throw failure();
  return Object.freeze({fileUri: row.fileUri, format, title: row.title, ...(format === 'idx' ? {companionUri: row.companionUri as string} : {})});
}

/** This is the Apple bridge port only. The current playback source owns upload,
 * service authority, language/rights and its complete before/after fence. */
export const appleSubtitleFilePicker = Platform.OS === 'ios' && !Platform.isTV && typeof bridge?.pick === 'function' && typeof bridge.cancel === 'function' && typeof bridge.discard === 'function' && typeof ids?.newRequestId === 'function' ? Object.freeze({
  discard,
  async pick(signal: AbortSignal): Promise<PickedFile | undefined> {
    if (signal.aborted) return;
    let requestId: string | undefined, started = false;
    let cancelled!: (value: {cancelled: true}) => void;
    const aborted = new Promise<{cancelled: true}>(resolve => {cancelled = resolve;});
    const abort = () => {
      cancelled({cancelled: true});
      if (started && requestId) {
        try { void Promise.resolve(bridge.cancel!(requestId)).catch(() => {}); } catch {}
      }
    };
    signal.addEventListener('abort', abort, {once: true});
    try {
      if (signal.aborted) return;
      const identity = await Promise.race([Promise.resolve(ids.newRequestId!()).then(value => ({value})), aborted]);
      if ('cancelled' in identity || signal.aborted) return;
      if (typeof identity.value !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(identity.value)) throw failure();
      requestId = identity.value; started = true;
      const result = await Promise.race([Promise.resolve(bridge.pick!(requestId)).then(value => {
        if (signal.aborted) { void discardResult(value); return {cancelled: true as const}; }
        return {value};
      }), aborted]);
      if ('cancelled' in result) return;
      if (signal.aborted) { await discardResult(result.value); return; }
      try { return selected(result.value); } catch (error) { await discardResult(result.value); throw error; }
    } catch (error) {
      if (signal.aborted || error && typeof error === 'object' && 'code' in error && error.code === 'cancelled') return;
      throw failure();
    } finally {
      signal.removeEventListener('abort', abort);
    }
  },
}) : undefined;
