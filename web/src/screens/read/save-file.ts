import type {ReadingSession} from '@portico/client-core';
import {randomId} from '@core/random-id.ts';

/** Save only fully received, checked original bytes. No public network URL,
 * native download receipt, detached server transfer or second Reader session. */
export async function saveReadingFile(session: ReadingSession, signal: AbortSignal, current: () => boolean): Promise<void> {
  const check = () => {
    if (signal.aborted || session.signal.aborted || !current()) throw new DOMException('Reading source retired', 'AbortError');
  };
  check();
  const bytes = await session.saveFile(randomId(), signal);
  check();
  if (!(bytes.buffer instanceof ArrayBuffer)) throw new Error('invalid_response');
  const blob = new Blob([bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength)], {type: 'application/octet-stream'});
  const url = URL.createObjectURL(blob);
  try {
    check();
    const link = document.createElement('a');
    link.href = url; link.download = 'publication'; link.rel = 'noopener noreferrer';
    link.referrerPolicy = 'no-referrer'; link.hidden = true;
    document.body.appendChild(link);
    try {check(); link.click();} finally {link.remove();}
  } catch (error) {URL.revokeObjectURL(url); throw error;}
  // The browser owns already-received bytes after the explicit click, while
  // the temporary object URL remains bounded and is never persisted.
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
