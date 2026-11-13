/** The kit cover hero: one table of sizes, and a cover drawn whole in its own shape (never cropped). */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const style = (node: any) => Object.assign({}, ...[node?.props?.style].flat(Infinity).filter(Boolean));
const colors = {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', accentStrong: '#5cb3e2', raised: '#112631', raisedHigh: '#1a3442', shadow: '#000'};
const type = (_theme: unknown, fontSize: number, fontWeight: number) => ({fontSize, fontWeight});

async function kit() {
  const ui = hooks(), theme = {isTV: false, platform: 'phone', colors};
  const module = await screenModule(new URL('../src-next/kit/layout/CoverHero.tsx', import.meta.url), {
    react: ui.react, 'react-native': {Text: 'Text', View: 'View'}, '../artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork', PersonCircle: 'PersonCircle'},
    '../controls/Focusable': {Focusable: 'Focusable'}, '../browse/presentation': {browseType: type}, '../theme': {useTheme: () => theme},
  });
  return {ui, CoverHero: module.CoverHero};
}

test('a square cover is drawn whole in its own shape inside its slot, portrait or landscape', async () => {
  const {ui, CoverHero} = await kit();
  const hero = ui.render(() => CoverHero({layout: 'stack', shape: 'square', title: 'Book', covers: [{key: 'c', source: {cacheKey: 'art'}, fallback: 'Generated'}], glow: {cacheKey: 'glow'}}));
  const whole = nodes(hero).find(node => node.type?.name === 'WholeCover');
  const draw = () => nodes(ui.render(() => whole.type(whole.props)));
  let drawn = draw(), artwork = drawn.find(node => node.type === 'Artwork');
  assert.deepEqual([style(drawn[0]).width, style(drawn[0]).height], [232, 232], 'the slot is the bound');
  assert.deepEqual([artwork.props.width, artwork.props.height], [232, 232], 'it fills the slot until the picture is in');
  artwork.props.onSize({width: 1000, height: 1500}); drawn = draw(); artwork = drawn.find(node => node.type === 'Artwork');
  assert.deepEqual([artwork.props.width, artwork.props.height], [155, 232], 'a 2:3 cover keeps its own shape');
  assert.deepEqual([style(drawn[0]).width, style(drawn[0]).height], [232, 232], 'the slot keeps its size');
  artwork.props.onSize({width: 1500, height: 1000}); artwork = draw().find(node => node.type === 'Artwork');
  drawn = draw(); artwork = drawn.find(node => node.type === 'Artwork');
  assert.deepEqual([artwork.props.width, artwork.props.height], [232, 155], 'a landscape cover keeps its own shape');
  const glow = drawn.find(node => node.props.children?.type === 'AmbientArt' || [node.props.children].flat().some((child: any) => child?.type === 'AmbientArt'));
  assert.deepEqual([style(glow).width, style(glow).height], [264, 177], 'the glow follows the drawn cover, not the square slot');
});

test('no picture draws the generated cover; an author is the person circle with their initials', async () => {
  const {ui, CoverHero} = await kit();
  const hero = ui.render(() => CoverHero({layout: 'stack', shape: 'square', title: 'Book', covers: [{key: 'c', source: undefined, fallback: 'Generated'}]}));
  const whole = nodes(hero).find(node => node.type?.name === 'WholeCover'), drawn = nodes(ui.render(() => whole.type(whole.props)));
  assert.equal(drawn.some(node => node.type === 'Artwork'), false);
  assert.ok(drawn.some(node => [node.props.children].flat().includes('Generated')));
  const author = nodes(ui.render(() => CoverHero({layout: 'stack', shape: 'person', title: 'Andy Weir', person: {covers: [], initials: 'AW'}})));
  const circle = author.find(node => node.type === 'PersonCircle');
  assert.deepEqual([circle.props.size, circle.props.initials], [150, 'AW']);
});

test('one table of sizes: a phone stacks, a wide tablet draws the row', async () => {
  const {ui, CoverHero} = await kit();
  const title = (layout: string, shape: string) => style(nodes(ui.render(() => CoverHero({layout, shape, title: 'Title', covers: [{key: 'c', source: {cacheKey: 'art'}, fallback: null}]})))
    .find(node => node.props.accessibilityRole === 'header')).fontSize;
  assert.deepEqual([title('stack', 'portrait'), title('stack', 'square'), title('stack', 'fan'), title('row', 'portrait'), title('row-wide', 'portrait')], [40, 30, 34, 66, 84]);
  const row = ui.render(() => CoverHero({layout: 'row-wide', shape: 'portrait', title: 'Title', covers: [{key: 'c', source: {cacheKey: 'art'}, fallback: null}]}));
  assert.equal(style(row).flexDirection, 'row');
  const whole = nodes(row).find(node => node.type?.name === 'WholeCover');
  assert.deepEqual([whole.props.width, whole.props.height], [206, 309]);
});
