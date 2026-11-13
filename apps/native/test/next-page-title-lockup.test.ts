import test from 'node:test';
import assert from 'node:assert/strict';
import * as design from '@portico/design';
import {createTheme, nativeType} from '../src-next/kit/theme.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// T11 (owner's most repeated complaint, lead's rulings 13 Nov): a page title is not cut at normal text size. The frames draw
// `.lockup` 46, `.lockup.md` 42, unclamped; one further step from the type scale (display, 34) for a very long title; up to
// four lines, and only past four does it end with an ellipsis.
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.flat(Infinity).filter(Boolean)) : style ?? {};
async function lockup(isTV = false) {
  const h = hooks(), theme = createTheme({isTV, width: isTV ? 1920 : 375});
  const module = await screenModule(new URL('../src-next/kit/layout/Lockup.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text'}, '@portico/design': design, '../theme': {nativeType, useTheme: () => theme}, './types': {},
  });
  return {module, draw: (props: Record<string, unknown>) => h.render(() => module.Lockup(props))};
}
const sixty = 'The Night the Ferry Did Not Come Back to the Northern Island';

test('a phone page title steps 46, 42 past 20 characters, 34 past 40, and wraps to four lines', async () => {
  const {module, draw} = await lockup();
  assert.equal(sixty.length, 60);
  assert.deepEqual(['Walk Show', 'Twelve Winters at the Observatory', sixty].map(title => flat(draw({title}).props.style).fontSize), [46, 42, design.typeScale.handheld.display.size]);
  assert.equal(design.typeScale.handheld.display.size, 34, 'the very long step is the type scale\'s display size, phone-read `.lockup.md`');
  assert.deepEqual([module.lockupStep('x'.repeat(20)), module.lockupStep('x'.repeat(21)), module.lockupStep('x'.repeat(41))], ['lockup', 'lockupLong', 'veryLong']);
  const title = draw({title: sixty});
  assert.deepEqual([title.props.numberOfLines, title.props.ellipsizeMode, title.props.accessibilityRole], [4, 'tail', 'header']);
  assert.equal(flat(draw({title: 'Walk Show', long: true}).props.style).fontSize, 42, 'a caller that already knows a title is long still gets the long step');
});

test('a page whose frame names its size keeps it, through the same title; TV keeps its own clamp', async () => {
  const phone = await lockup();
  const album = phone.draw({title: sixty, size: 30});
  assert.deepEqual([flat(album.props.style).fontSize, album.props.numberOfLines], [30, 4]);
  const tv = await lockup(true);
  const big = tv.draw({title: sixty, lines: 2});
  assert.deepEqual([flat(big.props.style).fontSize, big.props.numberOfLines], [design.typeScale.tenFoot.lockup.size, 2]);
});

test('a word wider than the line takes the next size down instead of breaking in the middle', async () => {
  const {module, draw} = await lockup();
  const name = 'Maximiliana Konstantinopoulou-Abernathy and the Northern Island Ensemble';
  let title = draw({title: name, size: 30});
  assert.equal(flat(title.props.style).fontSize, 30);
  title.props.onTextLayout({nativeEvent: {lines: [{text: 'Maximiliana '}, {text: 'Konstantinopoulo'}, {text: 'u-Abernathy and the '}, {text: 'Northern Island Ensemble'}]}});
  title = draw({title: name, size: 30});
  assert.equal(flat(title.props.style).fontSize, design.typeScale.handheld.title.size, 'the type scale\'s next title size (26)');
  assert.equal(title.props.onTextLayout, undefined, 'once: it does not keep stepping');
  const whole = draw({title: sixty});
  whole.props.onTextLayout({nativeEvent: {lines: [{text: 'The Night the Ferry Did '}, {text: 'Not Come Back to the '}, {text: 'Northern Island'}]}});
  assert.equal(flat(draw({title: sixty}).props.style).fontSize, 34, 'breaks at spaces change nothing');
  assert.deepEqual([46, 42, 34, 30].map(module.smallerStep), [42, 34, 26, 26]);
});
