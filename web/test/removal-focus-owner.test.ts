import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {RemovalFocusOwner, captureListFocusReturn} from '../src/kit/layout/removal-focus-owner.ts';
import {GridRemovalFocus, type GridRemovalFocusProps} from '../src/kit/layout/GridRemovalFocus.ts';

// UNRUN. Exercises the production DOM owner with structural element/event fixtures.
// Real React/Radix focus timing is covered separately by the browser regression source.
class ElementFixture extends EventTarget {
  children: ElementFixture[] = [];
  parentElement: ElementFixture | null = null;
  dataset: {layoutItem?: string} = {};
  isConnected = true; inert = false; hidden = false; button = false; tabIndex = false; keyboard = true;
  ownerDocument: EventTarget & {activeElement: ElementFixture | null; body: ElementFixture};
  constructor(document: ElementFixture['ownerDocument']) {super();this.ownerDocument = document;}
  append(...children: ElementFixture[]) {for (const child of children) {child.parentElement = this;this.children.push(child);}}
  contains(node: unknown): boolean {return node === this || this.children.some(child => child.contains(node));}
  matches(selector: string) {return selector === ':focus-visible' ? this.keyboard : selector === ':disabled' ? false : this.button;}
  hasAttribute(name: string) {return name === 'tabindex' ? this.tabIndex : name === 'inert' ? this.inert : false;}
  getAttribute(name: string) {return name === 'aria-hidden' && this.hidden ? 'true' : null;}
  closest() {for (let node: ElementFixture | null = this;node;node = node.parentElement) if (node.inert || node.hidden) return node;return null;}
  querySelectorAll(): ElementFixture[] {return this.children.flatMap(child => [...(child.button ? [child] : []), ...child.querySelectorAll()]);}
  getClientRects() {return this.isConnected ? [{}] : [];}
  focus() {if (!this.closest()) this.ownerDocument.activeElement = this;}
}
const dom = (node: ElementFixture) => node as unknown as HTMLElement;
function fixture(t: TestContext) {
  const overrides = {getComputedStyle: () => ({visibility: 'visible'}), HTMLElement: ElementFixture, Node: ElementFixture};
  for (const [name, value] of Object.entries(overrides)) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, {configurable: true, writable: true, value});
    t.after(() => {if (original) Object.defineProperty(globalThis, name, original);else Reflect.deleteProperty(globalThis, name);});
  }
  const document = Object.assign(new EventTarget(), {activeElement: null, body: undefined}) as unknown as ElementFixture['ownerDocument'];
  document.body = new ElementFixture(document);
  const root = new ElementFixture(document);root.tabIndex = true;
  const rows = ['a','b','c'].map(key => {const row = new ElementFixture(document);row.dataset.layoutItem = key;const button = new ElementFixture(document);button.button = true;row.append(button);return row;});
  root.append(...rows);
  const owner = new RemovalFocusOwner();owner.activate();owner.commit(dom(root));t.after(() => owner.dispose());
  const key = () => root.dispatchEvent(new Event('keydown'));
  const active = (index: number) => {rows[index].children[0].focus();key();};
  return {owner, root, rows, document, active, key};
}

test('beforeLeave moves keyboard focus before the caller makes the row inert', t => {
  const f = fixture(t);f.active(0);f.owner.beforeLeave(new Set(['b','c']));
  assert.equal(f.document.activeElement, f.rows[1].children[0]);assert.equal(f.rows[0].inert, false);
});
test('simultaneous removal skips another outgoing node and uses the original previous neighbour', t => {
  const f = fixture(t);f.active(2);f.owner.beforeLeave(new Set(['a']));
  assert.equal(f.document.activeElement, f.rows[0].children[0]);
});
test('pointer-only and unrelated removal leave the actual active control alone', t => {
  const f = fixture(t);f.active(0);f.root.dispatchEvent(new Event('pointerdown'));f.owner.beforeLeave(new Set(['b','c']));
  assert.equal(f.document.activeElement, f.rows[0].children[0]);f.active(1);f.owner.beforeLeave(new Set(['b','c']));assert.equal(f.document.activeElement, f.rows[1].children[0]);
});
test('a real portalled opening hands off from its original row and cannot restore that withdrawn opener', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document), button = new ElementFixture(f.document);button.button = true;portal.append(button);receipt.attach(dom(portal));button.focus();portal.dispatchEvent(new Event('keydown'));
  f.owner.beforeLeave(new Set(['b','c']));assert.equal(f.document.activeElement, f.rows[1].children[0]);receipt.restore();assert.equal(f.document.activeElement, f.rows[1].children[0]);receipt.dispose();
});
test('a sibling portal mutation detach preserves its original keyboard handoff before row inert', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document), button = new ElementFixture(f.document);button.button = true;portal.append(button);receipt.attach(dom(portal));button.focus();portal.dispatchEvent(new Event('keydown'));
  receipt.retireContent();portal.isConnected = false;f.document.activeElement = f.document.body;
  f.owner.beforeLeave(new Set(['b','c']));assert.equal(f.document.activeElement, f.rows[1].children[0]);assert.equal(f.rows[0].inert, false);receipt.restore();receipt.dispose();
});
test('outside pointer input cancels a retiring portal keyboard handoff', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document);receipt.attach(dom(portal));f.document.activeElement = portal;f.key();
  f.document.dispatchEvent(new Event('pointerdown'));receipt.retireContent();portal.isConnected = false;f.document.activeElement = f.document.body;
  f.owner.beforeLeave(new Set(['b','c']));receipt.restore();assert.equal(f.document.activeElement, f.document.body);receipt.dispose();
});
test('modal isolation defers only the captured handoff until close and refuses focus theft', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document), button = new ElementFixture(f.document);button.button = true;portal.append(button);receipt.attach(dom(portal));button.focus();portal.dispatchEvent(new Event('keydown'));
  f.root.hidden = true;f.owner.beforeLeave(new Set(['b','c']));assert.equal(f.document.activeElement, button);
  f.root.hidden = false;f.document.activeElement = f.document.body;receipt.restore();assert.equal(f.document.activeElement, f.rows[1].children[0]);receipt.dispose();
});
test('an unrelated focus change between modal retirement and close cancels its fallback', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document);receipt.attach(dom(portal));f.document.activeElement = portal;f.key();f.root.hidden = true;f.owner.beforeLeave(new Set(['b','c']));
  f.root.hidden = false;const outside = new ElementFixture(f.document);outside.button = true;outside.focus();receipt.restore();assert.equal(f.document.activeElement, outside);receipt.dispose();
});
test('a strict-effect replay never receives an old opening return', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  f.owner.activate();f.owner.commit(dom(f.root));f.document.activeElement = f.document.body;receipt.restore();assert.equal(f.document.activeElement, f.document.body);receipt.dispose();
});
test('a replacement node with the same key cannot become the old opening target', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const replacement = new ElementFixture(f.document);replacement.dataset.layoutItem = 'a';
  const button = new ElementFixture(f.document);button.button = true;replacement.append(button);replacement.parentElement = f.root;
  f.root.children[0] = replacement;f.owner.commit(dom(f.root));f.document.activeElement = f.document.body;
  receipt.restore();assert.equal(f.document.activeElement, f.document.body);receipt.dispose();
});
test('nested modal isolation leaves the original outer close handoff available', t => {
  const f = fixture(t);f.active(0);const outer = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const outerNode = new ElementFixture(f.document), opener = new ElementFixture(f.document);opener.button = true;outerNode.append(opener);outer.attach(dom(outerNode));opener.focus();
  const inner = captureListFocusReturn(dom(opener))!;const innerNode = new ElementFixture(f.document), button = new ElementFixture(f.document);button.button = true;innerNode.append(button);inner.attach(dom(innerNode));button.focus();innerNode.dispatchEvent(new Event('keydown'));
  f.root.hidden = true;f.owner.beforeLeave(new Set(['b','c']));inner.restore();assert.equal(f.document.activeElement, button);
  f.root.hidden = false;f.document.activeElement = f.document.body;outer.restore();assert.equal(f.document.activeElement, f.rows[1].children[0]);inner.dispose();outer.dispose();
});
test('root replacement invalidates both a queued portal return and its nested provenance', t => {
  const f = fixture(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const portal = new ElementFixture(f.document), button = new ElementFixture(f.document);portal.append(button);receipt.attach(dom(portal));
  f.owner.commit(dom(new ElementFixture(f.document)));f.document.activeElement = f.document.body;receipt.restore();assert.equal(f.document.activeElement, f.document.body);assert.equal(captureListFocusReturn(dom(button)), undefined);receipt.dispose();
});

// Actual before-mutation boundary methods, not a second acceptance predicate.
// These structural lifecycle calls do not claim real React DOM execution.
function gridBoundary(t: TestContext) {
  const f = fixture(t);
  const extent = new ElementFixture(f.document);extent.append(...f.rows);f.root.children = [extent];extent.parentElement = f.root;
  Object.defineProperty(f.root, 'firstElementChild', {get: () => f.root.children[0]});
  const props: GridRemovalFocusProps = {owner: f.owner, root: {current: dom(f.root) as HTMLDivElement}, children: null,
    generation: 1, accepted: true, keys: new Set(['a','b','c']), viewport: {width: 900, height: 600, top: 0}};
  const boundary = new GridRemovalFocus(props);boundary.componentDidMount();
  const update = (change: Partial<GridRemovalFocusProps>) => {
    const previous = boundary.props, next = {...previous, ...change};
    Object.defineProperty(boundary, 'props', {configurable: true, value: next});
    boundary.getSnapshotBeforeUpdate(previous);
  };
  t.after(() => boundary.componentWillUnmount());
  return {...f, boundary, update};
}
test('grid accepted generation hands focus off while the original sparse node is still connected', t => {
  const f = gridBoundary(t);f.rows[1].tabIndex = true;f.active(0);f.update({generation: 2, keys: new Set(['b','c'])});
  assert.equal(f.document.activeElement, f.rows[1].children[0]);assert.equal(f.rows[0].isConnected, true);
});
for (const [name, change] of [
  ['scroll window', {generation: 2, viewport: {width: 900, height: 600, top: 400}}],
  ['loading', {generation: 2, accepted: false}],
  ['same-generation page', {generation: 1}],
] as const) test(`grid ${name} change is not an admitted removal`, t => {
  const f = gridBoundary(t);f.active(0);f.update({...change, keys: new Set(['b','c'])});
  assert.equal(f.document.activeElement, f.rows[0].children[0]);
});
test('grid collection replacement retires old receipts without focusing the replacement window', t => {
  const f = gridBoundary(t);f.active(0);const receipt = captureListFocusReturn(dom(f.rows[0].children[0]))!;
  const replacement = new RemovalFocusOwner();t.after(() => replacement.dispose());
  f.update({owner: replacement, generation: 2, keys: new Set(['b','c'])});f.document.activeElement = f.document.body;
  receipt.restore();assert.equal(f.document.activeElement, f.document.body);receipt.dispose();
});
