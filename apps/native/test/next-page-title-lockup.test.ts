import test from 'node:test';
import assert from 'node:assert/strict';
import * as design from '@portico/design';
import {createTheme, nativeType} from '../src-next/kit/theme.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// T11 (owner's most repeated complaint, rulings 13 Nov): a page title is never cut. The frames draw `.lockup` 46, `.lockup.md`
// 42, unclamped; one further step from the type scale (display, 34) for a very long title. Past three lines it takes the scale's
// next size down (64, 58, 46, 42, 34) until the display size, and there it wraps as far as it needs: never an ellipsis.
const flat = (style: any): any => Array.isArray(style) ? Object.assign({}, ...style.flat(Infinity).filter(Boolean)) : style ?? {};
async function lockup(isTV = false, width = isTV ? 1920 : 375) {
  const h = hooks(), theme = createTheme({isTV, width});
  const module = await screenModule(new URL('../src-next/kit/layout/Lockup.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', StyleSheet: {flatten: flat}}, '@portico/design': design, '../theme': {nativeType, useTheme: () => theme}, './types': {},
  });
  return {module, draw: (props: Record<string, unknown>) => h.render(() => module.Lockup(props))};
}
const sixty = 'The Night the Ferry Did Not Come Back to the Northern Island';

test('a phone page title starts 46, 42 past 20 characters, 34 past 40, and is never cut', async () => {
  const {module, draw} = await lockup();
  assert.equal(sixty.length, 60);
  assert.deepEqual(['Walk Show', 'Twelve Winters at the Observatory', sixty].map(title => flat(draw({title}).props.style).fontSize), [46, 42, design.typeScale.handheld.display.size]);
  assert.equal(design.typeScale.handheld.display.size, 34, 'the very long step is the type scale\'s display size, phone-read `.lockup.md`');
  assert.deepEqual([module.lockupStep('x'.repeat(20)), module.lockupStep('x'.repeat(21)), module.lockupStep('x'.repeat(41))], ['lockup', 'lockupLong', 'veryLong']);
  const title = draw({title: sixty});
  assert.deepEqual([title.props.numberOfLines, title.props.accessibilityRole], [undefined, 'header'], 'no line limit, so no ellipsis');
  assert.equal(flat(draw({title: 'Walk Show', long: true}).props.style).fontSize, 42, 'a caller that already knows a title is long still gets the long step');
});

test('a page whose frame names its size keeps it, through the same title; TV keeps its own clamp', async () => {
  const phone = await lockup();
  const album = phone.draw({title: sixty, size: 30});
  assert.deepEqual([flat(album.props.style).fontSize, album.props.numberOfLines], [30, undefined]);
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
  assert.equal(flat(title.props.style).fontSize, module.smallerStep(30), 'the type scale\'s next title size below 30');
  assert.equal(module.smallerStep(30), 26);
  assert.equal(title.props.onTextLayout, undefined, 'the scale has no title size below 26');
  const whole = draw({title: sixty});
  whole.props.onTextLayout({nativeEvent: {lines: [{text: 'The Night the Ferry Did '}, {text: 'Not Come Back to the '}, {text: 'Northern Island'}]}});
  assert.equal(flat(draw({title: sixty}).props.style).fontSize, 34, 'breaks at spaces change nothing');
  assert.deepEqual(module.titleSizes(), [64, 58, 46, 42, 34, 26], 'the handheld scale\'s title sizes, 64 and 58 approved 13 Nov');
  assert.deepEqual([80, 64, 62, 46, 42, 34, 30].map(size => module.smallerStep(size)), [64, 58, 58, 42, 34, 26, 26], 'from the title\'s own size');
});

// Desk B on #3435: an iPad 1024 pt or wider lays out in the desktop column. Its page titles keep the four-line limit and the
// word rule (not the phone length steps), and a frame-sized title steps by the scale with its line height scaled.
test('a wide tablet title is never cut and keeps the word rule; a frame-sized title steps by the scale with its line height', async () => {
  const wide = await lockup(false, 1180);
  const title = wide.draw({title: sixty, size: 64, lineRatio: 1, tracking: -.045});
  assert.deepEqual([flat(title.props.style).fontSize, flat(title.props.style).lineHeight, title.props.numberOfLines], [64, 64, undefined]);
  assert.equal(typeof title.props.onTextLayout, 'function', 'the word rule runs in the desktop column too');
  title.props.onTextLayout({nativeEvent: {lines: [{text: 'The Night the Ferry Did Not Co'}, {text: 'me Back to the Northern Island'}]}});
  const stepped = flat(wide.draw({title: sixty, size: 64, lineRatio: 1, tracking: -.045}).props.style);
  const next = wide.module.smallerStep(64, 'desktop');
  assert.ok(next < 64 && next >= 26, `64 steps to the desktop scale's next title size (${next}), not to 46 by default`);
  assert.deepEqual([stepped.fontSize, stepped.lineHeight], [next, next], 'the line height keeps the frame\'s ratio at the new size');
  const album = wide.draw({title: 'Album', size: 80, lineRatio: 78.4 / 80, tracking: -.045});
  assert.deepEqual([flat(album.props.style).fontSize, flat(album.props.style).lineHeight, flat(album.props.style).letterSpacing], [80, 78.4, -.045 * 80]);
  const home = wide.draw({title: sixty});
  assert.equal(flat(home.props.style).fontSize, design.typeScale.desktop.lockup.size, 'no phone length steps on a wide tablet');
});

// Owner, 13 Nov (Home hero "The Extraordinary O…"): size down in steps, then wrap; never dots on the hero.
test('a title past three lines steps down the scale to the display size, then wraps; a caller\'s own size steps too', async () => {
  const {draw} = await lockup();
  const lines = (count: number) => ({nativeEvent: {lines: Array.from({length: count}, (_, index) => ({text: index < count - 1 ? 'word ' : 'end'}))}});
  const name = 'Twelve Winters at the Observatory';
  let title = draw({title: name});
  assert.equal(flat(title.props.style).fontSize, 42);
  title.props.onTextLayout(lines(3));
  assert.equal(flat(draw({title: name}).props.style).fontSize, 42, 'three lines is still fine');
  draw({title: name}).props.onTextLayout(lines(4));
  title = draw({title: name});
  assert.equal(flat(title.props.style).fontSize, 34, 'four lines: the next title size down');
  title.props.onTextLayout(lines(5));
  title = draw({title: name});
  assert.deepEqual([flat(title.props.style).fontSize, title.props.numberOfLines], [34, undefined], 'at the display size it wraps, uncut');
  // A tablet hero draws its column's size through its own style: the steps start there, line height and tracking scale with it.
  const tablet = await lockup(false, 834);
  let hero = tablet.draw({title: sixty, style: {fontSize: 58, lineHeight: 58 * 1.02, letterSpacing: -.04 * 58}});
  assert.equal(flat(hero.props.style).fontSize, 58);
  hero.props.onTextLayout(lines(4));
  hero = tablet.draw({title: sixty, style: {fontSize: 58, lineHeight: 58 * 1.02, letterSpacing: -.04 * 58}});
  const stepped = flat(hero.props.style);
  assert.equal(stepped.fontSize, 46);
  assert.ok(Math.abs(stepped.lineHeight - 46 * 1.02) < 1e-9 && Math.abs(stepped.letterSpacing - -.04 * 46) < 1e-9);
});
