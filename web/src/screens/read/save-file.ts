import type {ReadingSession} from '@portico/client-core';
import {randomId} from '@core/random-id.ts';

const buffered = 64 << 20;

/** Saves the book's own file, of any size (10-reading rule 45), and never replaces the reader
 * page. The browser downloads the file itself from the server's checked save address on the
 * connection's verified route: with a download link where the server serves this app, and in a
 * new tab from anywhere else, so a failed download shows there. An HTTPS page reaching the server
 * over plain HTTP keeps the in-page save up to 64 MB, since browsers warn about or block that
 * download; a larger file still uses the address and the browser's warning. */
export async function saveReadingFile(session: ReadingSession, signal: AbortSignal, current: () => boolean): Promise<void> {
  const check = () => {
    if (signal.aborted || session.signal.aborted || !current()) throw new DOMException('Reading source retired', 'AbortError');
  };
  check();
  const target = new URL(session.saveAddressNow());
  if (location.protocol === 'https:' && target.protocol === 'http:' && session.manifest.size <= buffered) return saveInPage(session, signal, check);
  // A tab opened later than the tap itself would be refused as a pop-up, so it opens now and is
  // sent to the file once the address is checked.
  const tab = target.origin === location.origin ? undefined : window.open('', '_blank');
  try {
    const address = await session.saveAddress(signal);
    check();
    if (tab) {tab.opener = null; tab.location.replace(address); return;}
    const link = document.createElement('a');
    // A tab the browser refused still never replaces the reader: the link opens one of its own.
    link.href = address; link.download = ''; link.rel = 'noopener noreferrer'; link.referrerPolicy = 'no-referrer'; link.hidden = true;
    if (target.origin !== location.origin) link.target = '_blank';
    document.body.appendChild(link);
    try {link.click();} finally {link.remove();}
  } catch (error) {tab?.close(); throw error;}
}

/** The whole file received and checked in the page, then handed to the browser to save. */
async function saveInPage(session: ReadingSession, signal: AbortSignal, check: () => void): Promise<void> {
  const bytes = await session.saveFile(randomId(), signal);
  check();
  if (!(bytes.buffer instanceof ArrayBuffer)) throw new Error('invalid_response');
  const blob = new Blob([bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength)], {type: 'application/octet-stream'});
  const url = URL.createObjectURL(blob);
  try {
    check();
    const link = document.createElement('a');
    link.href = url; link.download = `${session.manifest.metadata.title || 'book'}.${session.manifest.conversion?.originalFormat ?? session.manifest.format}`; link.rel = 'noopener noreferrer'; link.hidden = true;
    document.body.appendChild(link);
    try {link.click();} finally {link.remove();}
  } catch (error) {URL.revokeObjectURL(url); throw error;}
  // The browser owns the received bytes after the click; the temporary address is let go soon after.
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}
