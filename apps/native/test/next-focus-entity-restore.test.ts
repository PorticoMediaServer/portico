import test from 'node:test';
import assert from 'node:assert/strict';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';

// W-12 (immediate Back on the Movies grid): a natively windowed grid names its children by
// recycled slot. When the grid re-projects on return, the opener's slot can show another title;
// the restore must follow the title, never the slot, and wait while the title is not mounted.
test('W-12: a captured grid card is restored by its title, wherever its slot is now', () => {
  const registry = createFocusRegistry(), grid = 'library.movies.grid', focused: string[] = [];
  const host = (name: string) => ({requestTVFocus() {focused.push(name);}});
  registry.configure({id: grid, entry: 'slot-0', order: ['slot-0', 'slot-1', 'slot-2']});
  registry.attach({regionId: grid, childId: 'slot-1'}, host('slot-1: Blade Runner'), undefined, undefined, 'blade-runner');
  registry.didFocus({regionId: grid, childId: 'slot-1'});
  const opener = registry.capture()!;
  assert.equal(opener.entityId, 'blade-runner', 'the snapshot keeps the title it showed');

  // Back: the grid re-projects. Slot 1 now shows another film, and Blade Runner is not mounted yet.
  registry.attach({regionId: grid, childId: 'slot-1'}, null);
  registry.attach({regionId: grid, childId: 'slot-1'}, host('slot-1: Arrival'), undefined, undefined, 'arrival');
  assert.equal(registry.focus(opener), false, 'while the title is not mounted, nothing is focused (the return waits)');
  assert.deepEqual(focused, [], 'never the old slot showing a different title');

  // Blade Runner lands in slot 2.
  registry.attach({regionId: grid, childId: 'slot-2'}, host('slot-2: Blade Runner'), undefined, undefined, 'blade-runner');
  assert.equal(registry.focus(opener), true);
  assert.deepEqual(focused, ['slot-2: Blade Runner']);
});

test('an address without a title (a button, a menu opener) is focused by its child id as before', () => {
  const registry = createFocusRegistry(), focused: string[] = [];
  registry.configure({id: 'toolbar', entry: 'sort', order: ['sort']});
  registry.attach({regionId: 'toolbar', childId: 'sort'}, {requestTVFocus() {focused.push('sort');}});
  registry.didFocus({regionId: 'toolbar', childId: 'sort'});
  const opener = registry.capture()!;
  assert.equal(opener.entityId, undefined);
  assert.equal(registry.focus(opener), true);
  assert.deepEqual(focused, ['sort']);
});
