import {NativeModules, Platform} from 'react-native';

export type LivePickedDocument = Readonly<{data: string; title: string; bytes: number}>;
type DocumentBridge = {
  pick?: (requestId: string, kind: 'playlist' | 'guide') => Promise<unknown>;
  cancel?: (requestId: string) => Promise<unknown>;
};
type RuntimeIds = {newRequestId?: () => Promise<unknown>};

const bridge = NativeModules.PorticoDocumentFiles as Partial<DocumentBridge> | undefined;
const ids = NativeModules.PorticoRuntime as RuntimeIds | undefined;
const pickFile = bridge?.pick, cancelFile = bridge?.cancel, newRequestId = ids?.newRequestId;
const limit = 16 * 1024 * 1024;
const failure = () => new Error('Choose a playlist or guide file.');
const available = !Platform.isTV && typeof pickFile === 'function' && typeof cancelFile === 'function' && typeof newRequestId === 'function';

/** Read one M3U/XMLTV file through the platform's own document picker. Only the
 * text, display name and byte count cross the bridge; no path or lasting access
 * survives. A cancelled pick returns undefined; a phone/tablet picker is required. */
export const liveDocumentPicker = available ? Object.freeze({
  async pick(kind: 'playlist' | 'guide', signal: AbortSignal): Promise<LivePickedDocument | undefined> {
    if (signal.aborted) return;
    let requestId: string | undefined, started = false;
    let cancelled!: (value: {cancelled: true}) => void;
    const aborted = new Promise<{cancelled: true}>(resolve => {cancelled = resolve;});
    const abort = () => {
      cancelled({cancelled: true});
      if (started && requestId) {
        try {void Promise.resolve(cancelFile!(requestId)).catch(() => {});} catch {}
      }
    };
    signal.addEventListener('abort', abort, {once: true});
    try {
      if (signal.aborted) return;
      const identity = await Promise.race([Promise.resolve(newRequestId!()).then(value => ({value})), aborted]);
      if ('cancelled' in identity || signal.aborted) return;
      if (typeof identity.value !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(identity.value)) throw failure();
      requestId = identity.value;started = true;
      const result = await Promise.race([Promise.resolve(pickFile!(requestId, kind)).then(value => ({value})), aborted]);
      if ('cancelled' in result || signal.aborted || result.value == null) return;
      if (typeof result.value !== 'object' || Array.isArray(result.value)) throw failure();
      const row = result.value as Record<string, unknown>;
      if (Object.keys(row).some(key => !['data', 'title', 'bytes'].includes(key)) ||
        typeof row.data !== 'string' || !row.data.length || typeof row.title !== 'string' || !row.title.length || row.title.length > 160 ||
        typeof row.bytes !== 'number' || !Number.isSafeInteger(row.bytes) || row.bytes < 1 || row.bytes > limit) throw failure();
      return Object.freeze({data: row.data, title: row.title, bytes: row.bytes});
    } catch (error) {
      if (signal.aborted || error && typeof error === 'object' && 'code' in error && error.code === 'cancelled') return;
      throw failure();
    } finally {signal.removeEventListener('abort', abort);}
  },
}) : undefined;
