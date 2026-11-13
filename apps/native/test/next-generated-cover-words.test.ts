/** The generated cover's title breaks between words, never inside one: it shrinks so its longest word fits. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

async function generated() {
  const host = hooks(), react: any = {...host.react, createContext: (value: unknown) => ({value})}; react.default = react;
  return screenModule(new URL('../src-next/kit/artwork/Generated.tsx', import.meta.url), {
    react, 'react-native': {StyleSheet: {absoluteFillObject: {}}, Text: 'Text', View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../theme': {fontFamilies: {700: 'Bold', 800: 'ExtraBold'}, useTheme: () => ({colors: {text: '#fff', textSecondary: '#ccc', ink: '#000'}})},
    './Artwork': {BookSpine: 'BookSpine'}, './model': {mixColour: () => '#000'}, './Surfaces': {PaletteWash: 'PaletteWash'},
  });
}

test('a long word shrinks the title until it fits the column, so lines break only between words', async () => {
  const {coverTitleFontSize} = await generated();
  const column = (width: number) => width * (1 - 0.11 - 0.13);
  // "The Unill / ustrated Notebook" and "Photographi / c" on the phone walk: the 168 pt book cover.
  for (const title of ['The Unillustrated Notebook', 'A Photographic Field Guide to Quiet Worlds']) {
    const size = coverTitleFontSize(title, 168), longest = Math.max(...title.split(' ').map(word => word.length));
    assert.ok(longest * 0.66 * size <= column(168) + 0.01, `${title}: its longest word fits at ${size}`);
    assert.ok(size < 168 * 12.6 / 100, `${title} is drawn smaller than the authored size`);
  }
  // Short words keep the authored size.
  assert.equal(coverTitleFontSize('Dune', 168), 168 * 12.6 / 100);
  // A word too long to fit at half size stops shrinking there.
  assert.equal(coverTitleFontSize('Supercalifragilisticexpialidocious', 168), 168 * 12.6 / 200);
});
