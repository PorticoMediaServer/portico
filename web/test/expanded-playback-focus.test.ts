import test from 'node:test';
import assert from 'node:assert/strict';
import {ExpandedPlaybackFocus} from '../src/screens/watch/expanded-focus.ts';

// Structural DOM fixtures exercise the real focus owner, including ordering.
// Browser/React focus and assistive-technology acceptance remains a local check.
class ElementFixture {
  children: ElementFixture[] = [];
  parentElement: ElementFixture | null = null;
  isConnected = true; inert = false; hidden = false; disabled = false; tabIndex = -1;
  attributes = new Map<string, string>();
  constructor(readonly document: DocumentFixture, readonly tag = 'div') {}
  append(...children: ElementFixture[]) {for (const child of children) {child.parentElement = this; this.children.push(child);}}
  contains(node: unknown): boolean {return node === this || this.children.some(child => child.contains(node));}
  matches(selectors: string): boolean {
    return selectors.split(',').some(selector => {
      const excluded = [...selector.matchAll(/:not\(([^)]+)\)/g)].map(match => match[1]);
      if (excluded.some(part => this.matches(part))) return false;
      selector = selector.replace(/:not\([^)]+\)/g, '');
      if (selector === ':disabled') return this.disabled;
      if (selector === 'button' || selector === 'main' || selector === 'section') return this.tag === selector;
      const attributes = [...selector.matchAll(/\[([^=\]]+)(?:="([^"]*)")?\]/g)];
      return attributes.length > 0 && attributes.every(([, key, value]) => value === undefined ? this.hasAttribute(key) : this.getAttribute(key) === value);
    });
  }
  closest(selector: string): ElementFixture | null {for (let at: ElementFixture | null = this; at; at = at.parentElement) if (at.matches(selector)) return at; return null;}
  querySelectorAll(selector: string): ElementFixture[] {return this.children.flatMap(child => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]);}
  querySelector(selector: string) {return this.querySelectorAll(selector)[0] ?? null;}
  setAttribute(key: string, value: string) {this.attributes.set(key, value);}
  getAttribute(key: string) {return key === 'tabindex' ? String(this.tabIndex) : this.attributes.get(key) ?? null;}
  hasAttribute(key: string) {return key === 'inert' ? this.inert : key === 'hidden' ? this.hidden : this.attributes.has(key);}
  removeAttribute(key: string) {this.attributes.delete(key);}
  getClientRects() {return this.isConnected && !this.closest('[hidden]') ? [{}] : [];}
  focus() {
    if (this.disabled || !this.isConnected || this.closest('[inert],[hidden],[aria-hidden="true"]')) return;
    this.document.activeElement = this; this.document.dispatchEvent(new Event('focusin'));
  }
}
class DocumentFixture extends EventTarget {
  body = new ElementFixture(this, 'body');
  activeElement = this.body;
  querySelector(selector: string) {
    if (selector === '[data-playback-route] main') return this.body.querySelector('[data-playback-route]')?.querySelector('main') ?? null;
    return this.body.querySelector(selector);
  }
}
function fixture(kind: 'screen' | 'music' | 'audiobook' = 'screen') {
  const document = new DocumentFixture();
  const route = new ElementFixture(document), main = new ElementFixture(document, 'main'); route.setAttribute('data-playback-route', '');
  const previous = new ElementFixture(document, 'button'), opener = new ElementFixture(document, 'button'), next = new ElementFixture(document, 'button');
  route.append(main); main.append(previous, opener, next);
  const host = new ElementFixture(document), surface = new ElementFixture(document, 'section'), play = new ElementFixture(document, 'button');
  surface.setAttribute(`data-watch-${kind}`, ''); if (kind !== 'screen') play.setAttribute('data-playback-primary', '');
  host.append(surface); surface.append(play); document.body.append(route, host); opener.focus();
  const owner = new ExpandedPlaybackFocus(document as unknown as Document);
  const update = (expanded: boolean) => owner.update(host as unknown as HTMLElement, expanded);
  return {document, route, main, previous, opener, next, host, surface, play, owner, update};
}
for (const kind of ['screen', 'music', 'audiobook'] as const) test(`${kind} enters once, hides only the retained route, and restores its invoker`, () => {
  const f = fixture(kind); f.update(true);
  assert.equal(f.document.activeElement, kind === 'screen' ? f.surface : f.play);
  assert.equal(f.route.inert, true); assert.equal(f.route.getAttribute('aria-hidden'), 'true');
  assert.equal(f.main.isConnected, true);
  f.play.focus(); f.update(true); assert.equal(f.document.activeElement, f.play, 'clock/source updates do not reset focus');
  f.next.focus(); assert.equal(f.document.activeElement, f.play, 'covered controls cannot receive focus');
  f.update(false); assert.equal(f.route.inert, false); assert.equal(f.route.getAttribute('aria-hidden'), null);
  assert.equal(f.document.activeElement, f.opener); f.owner.dispose();
});
test('an unmounted invoker restores its next surviving neighbour, then previous', () => {
  for (const hasNext of [true, false]) {
    const f = fixture(); f.update(true); f.opener.isConnected = false; f.next.isConnected = hasNext;
    f.update(false); assert.equal(f.document.activeElement, hasNext ? f.next : f.previous); f.owner.dispose();
  }
});
test('a route is not hidden before an expanded surface actually commits', () => {
  const f = fixture(); f.surface.hidden = true; f.update(true); assert.equal(f.route.inert, false);
  f.surface.hidden = false; f.update(true); assert.equal(f.route.inert, true); assert.equal(f.document.activeElement, f.surface); f.owner.dispose();
});
test('a temporary source gap recovers lost focus without replacing the original invoker', () => {
  const f = fixture(); f.update(true); f.play.focus();
  f.surface.hidden = true; f.document.activeElement = f.document.body; f.update(true);
  assert.equal(f.document.activeElement, f.document.body);
  f.surface.hidden = false; f.update(true); assert.equal(f.document.activeElement, f.surface);
  f.play.focus(); f.update(true); assert.equal(f.document.activeElement, f.play);
  f.update(false); assert.equal(f.document.activeElement, f.opener); f.owner.dispose();
});
test('a replacement surface recovers disconnected focus while a portalled dialog keeps ownership', () => {
  const f = fixture('music'); f.update(true);
  f.surface.isConnected = false; f.play.isConnected = false;
  const replacement = new ElementFixture(f.document, 'section'), play = new ElementFixture(f.document, 'button');
  replacement.setAttribute('data-watch-music', ''); play.setAttribute('data-playback-primary', '');
  replacement.append(play); f.host.append(replacement); f.update(true);
  assert.equal(f.document.activeElement, play);
  const dialog = new ElementFixture(f.document), button = new ElementFixture(f.document, 'button');
  dialog.setAttribute('role', 'dialog'); dialog.append(button); f.document.body.append(dialog); button.focus();
  replacement.hidden = true; f.update(true); replacement.hidden = false; f.update(true);
  assert.equal(f.document.activeElement, button);
  f.update(false); assert.equal(f.document.activeElement, button); f.owner.dispose();
});
test('a portalled dialog keeps focus during open, updates and close', () => {
  const f = fixture(), dialog = new ElementFixture(f.document), button = new ElementFixture(f.document, 'button');
  dialog.setAttribute('role', 'dialog'); dialog.append(button); f.document.body.append(dialog); button.focus();
  f.update(true); assert.equal(f.document.activeElement, button); assert.equal(f.route.inert, true);
  f.update(false); assert.equal(f.document.activeElement, button); assert.equal(f.route.inert, false); f.owner.dispose();
});
test('a dialog inside the covered route cannot keep focus in the subtree being hidden', () => {
  const f = fixture(), dialog = new ElementFixture(f.document), button = new ElementFixture(f.document, 'button');
  dialog.setAttribute('role', 'dialog'); dialog.append(button); f.main.append(dialog); button.focus();
  f.update(true); assert.equal(f.document.activeElement, f.surface); assert.equal(f.route.inert, true);
  f.update(false); assert.equal(f.document.activeElement, button); f.owner.dispose();
});
test('cleanup restores the original route attributes and does not steal surviving outside focus', () => {
  const f = fixture(); f.route.setAttribute('aria-hidden', 'false'); f.update(true);
  const outside = new ElementFixture(f.document, 'button'); f.document.body.append(outside); outside.focus();
  f.owner.dispose(); assert.equal(f.route.getAttribute('aria-hidden'), 'false'); assert.equal(f.route.inert, false);
  assert.equal(f.document.activeElement, outside);
});
