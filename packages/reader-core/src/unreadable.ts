/** RD-2: the server rejected one EPUB asset or comic page as content (422
 * `reading_member_unreadable`, retry never) and kept the session open. The reader
 * shows that one member as unreadable and keeps every other page reachable. */
export const MEMBER_UNREADABLE = 'reading_member_unreadable';
export const memberUnreadable = (error: unknown): boolean =>
  !!error && typeof error === 'object' && (error as {code?: unknown}).code === MEMBER_UNREADABLE;

/** Reads one member; when the server rejected it as content and the reader can stand in for it,
 * returns `replace()` instead. Every other failure, and any failure without a stand-in, propagates. */
export async function orUnreadable<T, F>(read: () => Promise<T>, replace: (() => F) | undefined): Promise<T | F> {
  try {return await read();}
  catch (error) {if (!replace || !memberUnreadable(error)) throw error; return replace();}
}

/** A fixed-layout page surface that says the page can't be read, at the size of its
 * neighbours so turning past it doesn't jump. `text` comes from the host's catalogue. */
export function unreadablePageSurface(text: string, size: Readonly<{width: number; height: number}>) {
  return {width: size.width, height: size.height,
    async mount(target: HTMLElement, _scale: number, renderSignal: AbortSignal) {
      renderSignal.throwIfAborted();
      const note = target.ownerDocument.createElement('div');
      note.setAttribute('role', 'img'); note.setAttribute('aria-label', text); note.dataset.readerUnreadable = 'true';
      note.textContent = text;
      Object.assign(note.style, {position: 'absolute', inset: '0', display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: '8%', boxSizing: 'border-box', textAlign: 'center', font: '600 16px/1.4 system-ui, sans-serif', color: 'rgba(237,245,250,.72)', background: 'rgba(237,245,250,.06)'});
      target.append(note);
      return () => {note.remove();};
    },
    dispose() {}};
}

const escapeXml = (text: string) => text.replace(/[&<>"']/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&apos;'})[c]!);
/** A reflowable section that says this part of the book can't be read (`text` from the catalogue). */
export const unreadableSectionXhtml = (text: string) => `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title></title></head><body><p data-reader-unreadable="true" style="margin:30vh 0 0;text-align:center;opacity:.72">${escapeXml(text)}</p></body></html>`;
