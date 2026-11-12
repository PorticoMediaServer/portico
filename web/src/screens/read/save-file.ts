import type {ReadingSession} from '@portico/client-core';

/** Saves the book's own file, of any size (10-reading rule 45): the browser follows the server's
 * save address and downloads the file itself under its own name, so nothing is held in the page.
 * The address belongs to this book and this sign-in, and only a profile allowed to download has one. */
export async function saveReadingFile(session: ReadingSession, signal: AbortSignal, current: () => boolean): Promise<void> {
  const check = () => {
    if (signal.aborted || session.signal.aborted || !current()) throw new DOMException('Reading source retired', 'AbortError');
  };
  check();
  const address = await session.saveAddress(signal);
  check();
  const link = document.createElement('a');
  link.href = address; link.rel = 'noopener noreferrer'; link.referrerPolicy = 'no-referrer'; link.hidden = true;
  document.body.appendChild(link);
  try {link.click();} finally {link.remove();}
}
