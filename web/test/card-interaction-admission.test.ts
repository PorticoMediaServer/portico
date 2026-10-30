import test from 'node:test';
import assert from 'node:assert/strict';
import {createCardInteractionOwner, createCardInteractionBinding, captureCardInteractionRetirement, retireCardInteractions} from '../src/kit/cards/interaction-admission.ts';

// Unit-level physical-node doubles, not browser/keyboard qualification. UNRUN.
class Node {
  isConnected = true;inert = false;
  attrs = new Map<string, string>();children: Node[] = [];
  closest() {return this.inert ? this : null;}
  setAttribute(key: string, value: string) {this.attrs.set(key, value);}
  removeAttribute(key: string) {this.attrs.delete(key);}
  querySelectorAll() {return this.children.filter(node => node.attrs.has('data-card-interaction'));}
  get element() {return this as unknown as HTMLElement;}
}
function fixture() {
  const owner = createCardInteractionOwner(), row = {}, node = new Node(), paint: boolean[] = [];
  let current = true;
  const source = owner.source(row, {current: () => current, preview: enabled => paint.push(enabled)});
  return {owner, row, node, source, paint, invalidate: () => {current = false;}};
}
test('ordinary descriptor publication reuses the original source and callbacks', () => {
  const f = fixture(), calls: boolean[] = [];
  const published = f.owner.source(f.row, {current: () => false, preview: enabled => calls.push(enabled)});
  assert.equal(published, f.source);
  const admission = published.capture(f.node.element)!;
  admission.preview(true);assert.deepEqual(f.paint, [true]);assert.equal(calls.length, 0);
  assert.equal(admission.current(), true);assert.equal(admission.source.row, f.row);
  f.owner.dispose();
});
test('physical node replacement retires and clears the exact old admission synchronously', () => {
  const f = fixture(), first = f.source.capture(f.node.element)!;
  first.preview(true);let deadAtNotification = false;
  first.onRetire(() => {deadAtNotification = !first.current();});
  const second = f.source.capture(f.node.element)!;
  assert.equal(deadAtNotification, true);assert.notEqual(first, second);assert.deepEqual(f.paint, [true, false]);
  second.preview(true);first.preview(false);first.retire();assert.deepEqual(f.paint, [true, false, true]);
  assert.equal(second.current(), true);f.owner.dispose();
});
test('an outgoing row cannot clear a different row preview from the same owner', () => {
  const f = fixture(), other = new Node(), seen: boolean[] = [];
  const secondSource = f.owner.source({}, {current: () => true, preview: value => seen.push(value)});
  const a = f.source.capture(f.node.element)!, b = secondSource.capture(other.element)!;
  a.preview(true);b.preview(true);a.retire();assert.deepEqual(f.paint, [true]);assert.deepEqual(seen, [true]);
  b.retire();assert.deepEqual(seen, [true, false]);f.owner.dispose();
});
test('a producer clear releases old preview ownership before later publication', () => {
  const f = fixture(), a = f.source.capture(f.node.element)!, other = new Node();
  a.preview(true);f.owner.resetPreview();
  const b = f.source.capture(other.element)!;b.preview(true);a.preview(false);a.retire();
  assert.deepEqual(f.paint, [true, true]);b.retire();assert.deepEqual(f.paint, [true, true, false]);
});
test('before-inert subtree retirement clears and notifies before DOM withdrawal', () => {
  const f = fixture(), root = new Node();root.children.push(f.node);
  const admission = f.source.capture(f.node.element)!;admission.preview(true);
  let notified = 0;admission.onRetire(() => {assert.equal(f.node.inert, false);notified++;});
  retireCardInteractions(root.element);f.node.inert = true;
  assert.equal(notified, 1);assert.deepEqual(f.paint, [true, false]);assert.equal(admission.current(), false);
  assert.equal(f.source.capture(f.node.element), undefined);f.owner.dispose();
});
test('source retirement and disconnected nodes withdraw original receipts once', () => {
  const f = fixture(), admission = f.source.capture(f.node.element)!;admission.preview(true);
  let notified = 0;admission.onRetire(() => notified++);f.invalidate();f.owner.prune();
  f.owner.prune();admission.retire();assert.equal(notified, 1);assert.deepEqual(f.paint, [true, false]);
  const disconnected = fixture(), b = disconnected.source.capture(disconnected.node.element)!;
  b.preview(true);disconnected.node.isConnected = false;assert.equal(b.current(), false);
  assert.deepEqual(disconnected.paint, [true, false]);f.owner.dispose();disconnected.owner.dispose();
});
test('reentrant retirement cannot erase a newer physical mount or preview', () => {
  const f = fixture(), first = f.source.capture(f.node.element)!;
  let successor: ReturnType<typeof f.source.capture>;
  first.preview(true);first.onRetire(() => {successor = f.source.capture(f.node.element);successor!.preview(true);});
  // The outer capture refuses to replace a successor installed by retirement.
  assert.equal(f.source.capture(f.node.element), undefined);
  assert.equal(successor!.current(), true);first.retire();assert.equal(f.node.attrs.has('data-card-interaction'), true);
  assert.deepEqual(f.paint, [true, false, true]);f.owner.dispose();
});
test('a different producer on the same physical node retires the original owner', () => {
  const f = fixture(), first = f.source.capture(f.node.element)!;first.preview(true);
  const nextOwner = createCardInteractionOwner(), seen: boolean[] = [];
  const second = nextOwner.source({}, {current: () => true, preview: value => seen.push(value)}).capture(f.node.element)!;
  second.preview(true);f.owner.dispose();first.preview(false);
  assert.equal(first.current(), false);assert.equal(second.current(), true);assert.deepEqual(seen, [true]);
  nextOwner.dispose();assert.deepEqual(seen, [true, false]);
});
test('disposal is terminal and late subscribers observe retirement immediately', () => {
  const f = fixture(), admission = f.source.capture(f.node.element)!;
  f.owner.dispose();let observed = 0;admission.onRetire(() => observed++);
  assert.equal(observed, 1);assert.equal(f.source.capture(new Node().element), undefined);admission.preview(true);
  assert.equal(f.paint.length, 0);
});

test('ordinary descriptor commits keep one physical receipt and explicit withdrawal cannot recapture it', () => {
  const f = fixture(), binding = createCardInteractionBinding();
  const first = binding.commit(f.source, f.node.element, false)!;first.preview(true);
  assert.equal(binding.commit(f.source, f.node.element, false), first);
  retireCardInteractions(f.node.element);
  assert.equal(binding.commit(f.source, f.node.element, false), undefined);
  assert.deepEqual(f.paint, [true, false]);assert.equal(first.live, false);
  binding.commit(f.source, f.node.element, true);
  const returned = binding.commit(f.source, f.node.element, false)!;
  assert.notEqual(returned, first);assert.equal(returned.current(), true);binding.release();f.owner.dispose();
});

test('actual ref-node replacement and replay cleanup capture fresh original receipts', () => {
  const f = fixture(), binding = createCardInteractionBinding(), other = new Node();
  const first = binding.commit(f.source, f.node.element, false)!;let cancelled = 0;
  first.onRetire(() => cancelled++);
  const second = binding.commit(f.source, other.element, false)!;
  assert.equal(first.live, false);assert.equal(second.node, other.element);assert.equal(cancelled, 1);
  binding.release();assert.equal(second.live, false);
  const replay = binding.commit(f.source, other.element, false)!;
  assert.notEqual(replay, second);assert.equal(replay.live, true);binding.release();f.owner.dispose();
});

test('committed owner replacement withholds the old receipt before successor capture', () => {
  const f = fixture(), binding = createCardInteractionBinding(), otherOwner = createCardInteractionOwner();
  const first = binding.commit(f.source, f.node.element, false)!;first.preview(true);
  const source = otherOwner.source({}, {current: () => true, preview() {}});
  const second = binding.commit(source, f.node.element, false)!;
  assert.equal(first.live, false);assert.notEqual(second.source.owner, first.source.owner);
  assert.equal(second.current(), true);assert.deepEqual(f.paint, [true, false]);binding.release();f.owner.dispose();otherOwner.dispose();
});

test('subtree retirement snapshots original receipts before any reentrant replacement', () => {
  const f = fixture(), root = new Node(), other = new Node();root.children.push(f.node, other);
  const first = f.source.capture(f.node.element)!, oldOther = f.source.capture(other.element)!;
  let successor: ReturnType<typeof f.source.capture>;
  first.onRetire(() => {successor = f.source.capture(other.element);});
  retireCardInteractions(root.element);
  assert.equal(first.live, false);assert.equal(oldOther.live, false);
  assert.equal(successor!.live, true);assert.equal(successor!.current(), true);f.owner.dispose();
});

test('captured multi-row withdrawal cannot adopt a successor created by focus handoff', () => {
  const f = fixture(), other = new Node();
  const first = f.source.capture(f.node.element)!, oldOther = f.source.capture(other.element)!;
  const withdraw = captureCardInteractionRetirement([f.node.element, other.element]);
  // Represents a caller focus callback replacing another physical admission.
  const successor = f.source.capture(other.element)!;successor.preview(true);
  withdraw();withdraw();
  assert.equal(first.live, false);assert.equal(oldOther.live, false);assert.equal(successor.live, true);
  assert.deepEqual(f.paint, [true]);f.owner.dispose();
});
