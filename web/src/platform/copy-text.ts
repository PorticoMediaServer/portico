/** Call directly from a user gesture with already prepared text. The selection
 * copy runs synchronously, including on plain HTTP where Clipboard is absent.
 * Never wait for a rejected async clipboard write before trying this fallback:
 * Safari may have retired the gesture by then. Nothing is kept in the DOM. */
export async function copyText(text: string, source?: HTMLElement): Promise<void> {
  if (typeof document.execCommand === 'function') {
    const focused = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const selection = document.getSelection();
    const ranges = selection ? Array.from({length: selection.rangeCount}, (_, i) => selection.getRangeAt(i).cloneRange()) : [];
    const input = focused instanceof HTMLInputElement || focused instanceof HTMLTextAreaElement ? focused : undefined;
    const start = input?.selectionStart, end = input?.selectionEnd, direction = input?.selectionDirection;
    const field = document.createElement('textarea');
    field.value = text;field.readOnly = true;field.tabIndex = -1;
    field.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;font-size:var(--type-fixed-size-16px)';
    // Safari need not focus a clicked button. Its actual gesture source still
    // identifies the modal whose focus trap must contain the temporary field.
    const modal = '[role="dialog"], [role="alertdialog"]';
    const parent = source?.closest(modal) ?? focused?.closest(modal) ?? document.body;
    try {
      parent.appendChild(field);field.focus({preventScroll: true});field.select();field.setSelectionRange(0, text.length);
      if (document.execCommand('copy')) return;
    } catch {
      // A browser can expose the legacy command but refuse it. Try the modern
      // API below, still within this same synchronous gesture.
    } finally {
      field.value = '';field.remove();
      if (focused?.isConnected) focused.focus({preventScroll: true});
      if (input && start !== null && start !== undefined && end !== null && end !== undefined) input.setSelectionRange(start, end, direction ?? undefined);
      if (selection) {selection.removeAllRanges();for (const range of ranges) selection.addRange(range);}
    }
  }
  if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('clipboard_unavailable');
  return navigator.clipboard.writeText(text);
}
