import {test} from 'node:test';
import assert from 'node:assert/strict';
import {unprefixEpubCss,lastLinearIndex} from '../src/epub/vendor/compat.js';

test('EPUB prefix conversion retains upstream token boundaries without lookbehind',()=>{
  const samples=['-epub-unprefixed-at-start', 'p {-epub-hyphens:auto;-epub-text-align-last:justify}',
    'a { -EPUB-WORD-BREAK:normal; x-epub-foo:x }', 'a\n-epub-writing-mode:vertical-rl',
    'p{content:"-epub-keep";url:example-epub-keep}', 'p{--epub-keep:x;}-epub-next:x'];
  for(const css of samples)assert.equal(unprefixEpubCss(css),css.replace(/(?<=[{\s;])-epub-/gi,''));
});

test('last linear section preserves explicit non-linear exclusions, omitted facts and empty books',()=>{
  for(const sections of [[],[{linear:'no'}],[{}, {linear:'no'}],[{linear:'yes'},{linear:'no'},{}]])
    assert.equal(lastLinearIndex(sections),sections.findLastIndex(section=>section.linear!=='no'));
});
