import {NativeModules, Platform} from 'react-native';
import {routeBase64} from '../../../../packages/client-core/src/route-identity';
import {nativeRouteCrypto} from './server-connections';

type File = Readonly<{fileName: string; markdown: string}>;
type Sink = {saveHighlights: (id: string, name: string, markdown: string) => Promise<'saved' | 'cancelled'>; cancelHighlights: (id: string) => void};
/** Fixed Markdown-only OS sink. No URL, path, MIME, network or shared-file port. */
export function readingHighlightsSavePort() {
  const sink: Sink | undefined = NativeModules.PorticoHighlightsSave;
  if (Platform.isTV || Platform.OS !== 'android' && Platform.OS !== 'ios' || !sink?.saveHighlights || !sink.cancelHighlights) return undefined;
  return async (file: File, signal: AbortSignal, isCurrent: () => boolean): Promise<'saved' | 'cancelled'> => {
    const current = () => !signal.aborted && isCurrent();
    if (!current() || !/^[A-Za-z0-9 -]{1,64}-highlights\.md$/.test(file.fileName) || !file.markdown.startsWith('# Highlights\n') || new TextEncoder().encode(file.markdown).length > 4 * 1024 * 1024) throw new Error('reading_export_retired');
    const id = routeBase64(nativeRouteCrypto.random(24));
    const abort = () => sink.cancelHighlights(id);
    signal.addEventListener('abort', abort, {once: true});
    try {
      if (!current()) throw new Error('reading_export_retired');
      const result = await sink.saveHighlights(id, file.fileName, file.markdown);
      if (!current()) throw new Error('reading_export_retired');
      if (result !== 'saved' && result !== 'cancelled') throw new Error('reading_export_invalid_result');
      return result;
    } finally {signal.removeEventListener('abort', abort); sink.cancelHighlights(id);}
  };
}
