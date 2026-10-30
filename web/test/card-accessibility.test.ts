import test from 'node:test';
import assert from 'node:assert/strict';
import {cardDescription} from '../src/kit/cards/accessibility.ts';

test('same-title cards retain their own caption, detail and state in the description', () => {
  const original = {model: {title: 'Dune', caption: [{id: 'year', role: 'context' as const, text: '1984'}], detail: [{id: 'remaining', role: 'status' as const, text: '45 min left'}]},
    extraCaption: [[{id: 'author', role: 'identity' as const, text: 'Frank Herbert'}]]};
  const remake = {...original, model: {...original.model, caption: [{id: 'year', role: 'context' as const, text: '2021'}]}};
  assert.equal(cardDescription(original, ['New', 'Watched']), '1984, 45 min left, Frank Herbert, New, Watched');
  assert.equal(cardDescription(remake, ['New', 'Watched']), '2021, 45 min left, Frank Herbert, New, Watched');
  assert.equal(original.model.title, remake.model.title);
});

test('missing facts stay absent and repeated state is announced once', () => {
  assert.equal(cardDescription({model: {title: 'A title', caption: []}}), '');
  assert.equal(cardDescription({model: {title: 'A title', caption: [{id: 'state', role: 'status', text: 'Finished'}]}}, [undefined, '', 'Finished']), 'Finished');
});
