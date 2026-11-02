import test from 'node:test';
import assert from 'node:assert/strict';
import {inlineMarkup} from '../../packages/client-core/src/presentation/inline-markup.ts';

const flat = (value: string) => inlineMarkup(value).map(run => run.kind === 'break' ? '\n' : run.strong ? `*${run.text}*` : run.emphasis ? `_${run.text}_` : run.text).join('');

test('book descriptions decode entities and keep their bold, italics and breaks', () => {
  assert.equal(flat('<b>The #1</b> <i>Harbor Times</i> bestseller.<br />A map in a bottle &amp; a bay.'), '*The #1* _Harbor Times_ bestseller.\nA map in a bottle & a bay.');
  assert.equal(flat('It&#8217;s &quot;here&quot; &#x2014; now&hellip;'), 'It’s "here" — now…');
});
test('paragraphs become separate lines without a trailing gap', () => {
  assert.equal(flat('<p>One</p><p>Two</p>'), 'One\n\nTwo');
  assert.equal(flat('Lead<p>Next</p>'), 'Lead\nNext');
});
test('text that never carried markup reads exactly as before', () => {
  assert.equal(flat('R&D, 5 < 6 and &unknown; stay; <div> too'), 'R&D, 5 < 6 and &unknown; stay; <div> too');
  assert.equal(flat('&#0; &#xD800; stay literal'), '&#0; &#xD800; stay literal');
});
