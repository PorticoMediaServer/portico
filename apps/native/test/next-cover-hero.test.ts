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
  // A picture that is slow, refused or broken: the kit image draws its fallback, which is the generated cover.
  const withPicture = nodes(ui.render(() => CoverHero({layout: 'stack', shape: 'square', title: 'Book', covers: [{key: 'c', source: {cacheKey: 'gone'}, fallback: 'Generated'}]})))
    .find(node => node.type?.name === 'WholeCover');
  assert.equal(nodes(ui.render(() => withPicture.type(withPicture.props))).find(node => node.type === 'Artwork').props.fallback, 'Generated');
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

test('a fan places one, two or three volumes as phone-read 4 draws them, each drawn whole', async () => {
  const {ui, CoverHero} = await kit();
  const placed = (count: number) => {
    const covers = Array.from({length: count}, (_, index) => ({key: `v${index}`, source: {cacheKey: `art${index}`}, fallback: `Generated ${index}`}));
    const fan = nodes(ui.render(() => CoverHero({layout: 'stack', shape: 'fan', title: 'Series', covers})));
    const slots = fan.filter(node => node.type?.name === 'WholeCover');
    return slots.map(slot => {
      const holder = fan.find(node => [node.props.children].flat().includes(slot))!;
      return {left: style(holder).left, top: style(holder).top, rotate: style(holder).transform?.[0]?.rotate, size: [slot.props.width, slot.props.height]};
    });
  };
  assert.deepEqual(placed(1), [{left: 76, top: 12, rotate: '0deg', size: [128, 192]}]);
  assert.deepEqual(placed(2).map(p => [p.left, p.top, p.rotate]), [[36, 12, '-5deg'], [116, 12, '5deg']]);
  assert.deepEqual(placed(3).map(p => [p.left, p.top, p.rotate]), [[4, 18, '-9deg'], [72, 6, '-2deg'], [146, 14, '7deg']]);
  assert.equal(placed(5).length, 3, 'never more than three volumes');
  // Each volume is the whole-cover piece: a landscape volume keeps its own shape inside its 128 x 192 slot.
  const fan = nodes(ui.render(() => CoverHero({layout: 'stack', shape: 'fan', title: 'Series', covers: [{key: 'v', source: {cacheKey: 'wide'}, fallback: 'Generated'}]})));
  const slot = fan.find(node => node.type?.name === 'WholeCover');
  const draw = () => nodes(ui.render(() => slot.type(slot.props)));
  draw().find(node => node.type === 'Artwork').props.onSize({width: 1500, height: 1000});
  const artwork = draw().find(node => node.type === 'Artwork');
  assert.deepEqual([artwork.props.width, artwork.props.height, artwork.props.fallback], [128, 85, 'Generated']);
});
