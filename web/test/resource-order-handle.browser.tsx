import {flushSync} from 'react-dom';
import {createRoot} from 'react-dom/client';
import {ResourceOrderHandle} from '../src/screens/saved/ResourceOrderHandle';

/** Run in the complete project's Vite-served browser, for example:
 * await (await import('/test/resource-order-handle.browser.tsx')).checkResourceOrderHandleKeyboard()
 * Uses the real component, React event propagation and DOM focus. The caller
 * owns the browser/server lifetime; this fixture sends no resource mutation. */
export function checkResourceOrderHandleKeyboard() {
  const previousFocus = document.activeElement;
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host), moves: {entryId: string; targetId: string}[] = [], parentKeys: string[] = [];
  const check = (condition: boolean, message: string) => {if (!condition) throw new Error(message);};
  const ids = ['first', 'hidden', 'second'];
  const onMove = (entryId: string, targetId: string) => {moves.push({entryId, targetId});};
  try {
    flushSync(() => root.render(<div data-resource-order tabIndex={-1} onKeyDown={event => {
      parentKeys.push(event.key);
      // WindowedGrid also takes focus when an arrow reaches its bubble handler.
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') event.currentTarget.focus();
    }}>
      <div data-resource-entry="first"><ResourceOrderHandle entryId="first" entryIds={ids} title="First" disabled={false} onMove={onMove}/></div>
      <div>Unavailable entry</div>
      <div data-resource-entry="second"><ResourceOrderHandle entryId="second" entryIds={ids} title="Second" disabled={false} onMove={onMove}/></div>
    </div>));
    const handle = host.querySelector<HTMLButtonElement>('[data-resource-entry="first"] button')!;
    const second = host.querySelector<HTMLElement>('[data-resource-entry="second"]')!;
    const press = (key: string) => {
      const event = new KeyboardEvent('keydown', {key, bubbles: true, cancelable: true});
      flushSync(() => {handle.dispatchEvent(event);});
      check(event.defaultPrevented, `${key} must be consumed by the handle`);
      check(document.activeElement === handle, `${key} must keep focus on the lifted handle`);
      check(parentKeys.length === 0, `${key} must not reach the parent keyboard navigator`);
    };
    handle.focus();
    press(' ');
    check(handle.getAttribute('aria-pressed') === 'true', 'Space must lift the row');
    press('ArrowDown');
    check(handle.getAttribute('aria-pressed') === 'true', 'ArrowDown must not cancel the lift by blurring it');
    check(second.getAttribute('data-order-drop') === 'after', 'ArrowDown must target the next drawn row, past the opaque hidden occurrence');
    check(moves.length === 0, 'Choosing a target must not submit before drop');
    press('Enter');
    check(handle.getAttribute('aria-pressed') === 'false', 'Enter must finish the lift');
    check(!second.hasAttribute('data-order-drop'), 'Drop must remove its indicator');
    check(moves.length === 1 && moves[0].entryId === 'first' && moves[0].targetId === 'second', 'Drop must submit the original occurrence and chosen target exactly once');

    press(' ');press('ArrowDown');press('Escape');
    check(handle.getAttribute('aria-pressed') === 'false', 'Escape must cancel a lifted row');
    check(!second.hasAttribute('data-order-drop'), 'Cancel must remove its indicator');
    check(moves.length === 1, 'Cancelling must not submit another move');
    return {moves, parentKeys, focusStayedOnHandle: true};
  } finally {
    flushSync(() => root.unmount());
    host.remove();
    if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
  }
}
