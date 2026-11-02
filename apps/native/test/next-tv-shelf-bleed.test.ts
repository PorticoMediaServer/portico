import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {focus as focusSpec} from '../../../packages/design/src/components.ts';

const theme = {spec: {focus: focusSpec}};

async function shelf() {
  const h = hooks();
  const module = await screenModule(new URL('../src-next/tv/layout/TVShelf.tsx', import.meta.url), {
    react: h.react, 'react-native': {View: 'View'}, '../../kit/theme': {useTheme: () => theme}, '../../kit/layout/Row': {Row: 'Row'}, '../../kit/layout/types': {}});
  return {module, render: (itemWidth: number) => h.render(() => module.TVShelf({items: [], getKey: () => 'k', renderItem: () => null, itemWidth, itemHeight: 320})) as any};
}

test('W-11: a TV shelf reserves the focused card\'s growth and ring on both sides without moving its cards', async () => {
  const {module, render} = await shelf();
  const bleed = module.tvShelfBleed(theme, 384);
  // 1.08 scale on a 384 card grows 15.36 a side, plus the 4 + 4 ring at that scale.
  assert.equal(bleed, Math.ceil(384 * (focusSpec.scale.card.tv - 1) / 2 + (focusSpec.offsetOuter.tv + focusSpec.ringWidth.tv) * focusSpec.scale.card.tv));
  assert.ok(bleed >= 24, 'room for the episode card\'s ring');
  const tree = render(384), row = [tree.props.children].flat(Infinity).find((child: any) => child?.type === 'Row');
  assert.equal(tree.type, 'View'); assert.equal(tree.props.style.marginHorizontal, -bleed, 'the track is pulled out by the bleed');
  assert.ok(row, 'the row is inside the bleed');
  assert.equal(row.props.gutter, bleed, 'so a focused first card sits at the drawn edge with its ring inside the track');
  assert.equal(row.props.rightGutter, bleed);
});

test('a narrower card needs less room', async () => {
  const {module} = await shelf();
  assert.ok(module.tvShelfBleed(theme, 214) < module.tvShelfBleed(theme, 384));
});
