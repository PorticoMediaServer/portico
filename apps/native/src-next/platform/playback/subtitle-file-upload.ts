import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import type {NativePlaybackSubtitleFileUpload} from './subtitle-controls';

export type NativeSubtitlePickedFile = Readonly<{
  fileUri: string; format: 'srt' | 'vtt' | 'ass' | 'ssa' | 'sup' | 'idx'; title: string; companionUri?: string;
}>;
export type NativeSubtitleFilePicker = Readonly<{pick: (signal: AbortSignal) => Promise<NativeSubtitlePickedFile | undefined>; discard: (uris: readonly string[]) => Promise<unknown>}>;
export type NativeSubtitleFileLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
export type NativeSubtitleFileMetadata = Readonly<Pick<NativePlaybackSubtitleFileUpload, 'language' | 'rights' | 'scope' | 'offsetUs'>>;

/** A file picker borrows the existing current-source service. Neither a cancelled
 * native result nor a queued save may outlive its source or its calling sheet. */
export async function uploadPickedSubtitle(
  picker: NativeSubtitleFilePicker,
  save: (input: NativePlaybackSubtitleFileUpload, lifetime: NativeSubtitleFileLifetime) => Promise<void>,
  source: NativeSubtitleFileLifetime,
  caller: NativeSubtitleFileLifetime,
  metadata: NativeSubtitleFileMetadata,
): Promise<boolean> {
  const controller = new AbortController(), abort = () => controller.abort();
  const current = () => !controller.signal.aborted && source.isCurrent() && caller.isCurrent();
  source.signal.addEventListener('abort', abort, {once: true});
  caller.signal.addEventListener('abort', abort, {once: true});
  if (source.signal.aborted || caller.signal.aborted) controller.abort();
  const cancelled = Symbol('cancelled');
  let cancel!: () => void;
  const cancellation = new Promise<typeof cancelled>(resolve => {cancel = () => resolve(cancelled);});
  controller.signal.addEventListener('abort', cancel, {once: true});
  // Capture the caller's real language, rights, scope and correction before the
  // native await; no viewer defaults or synthetic file facts are introduced.
  const fields = Object.freeze({language: metadata.language, rights: metadata.rights, scope: metadata.scope, offsetUs: metadata.offsetUs});
  let picked: NativeSubtitlePickedFile | undefined, settled = false;
  const discard = async (file: NativeSubtitlePickedFile) => {
    try { await picker.discard([file.fileUri, ...(file.companionUri ? [file.companionUri] : [])]); } catch {}
  };
  try {
    if (!current()) return false;
    const result = await Promise.race([picker.pick(controller.signal).then(value => {
      if (settled && value) void discard(value);
      else picked = value;
      return value;
    }), cancellation]);
    if (result === cancelled || !picked || !current()) return false;
    const lifetime = {signal: controller.signal, isCurrent: current};
    const file = (uri: string, extension: string) => ({uri, name: decodeURIComponent(uri.split('/').at(-1) || `subtitle.${extension}`), type: 'application/octet-stream'});
    const main = file(picked.fileUri, picked.format), companion = picked.companionUri ? file(picked.companionUri, 'sub') : undefined;
    await save({...fields, file: main, fileName: main.name, format: picked.format, title: picked.title, ...(companion ? {companion, companionName: companion.name} : {})}, lifetime);
    if (!current()) return false;
    return true;
  } catch {
    if (!current()) return false;
    throw new Error(serviceText('player.subtitles.changeFailed'));
  } finally {
    settled = true;
    source.signal.removeEventListener('abort', abort);
    caller.signal.removeEventListener('abort', abort);
    controller.signal.removeEventListener('abort', cancel);
    controller.abort();
    if (picked) await discard(picked);
  }
}
