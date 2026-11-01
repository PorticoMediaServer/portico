import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk, 1 Nov: titles without a poster drew blank cards on TV and phone. They draw the
// generated cover of mock.html's "No artwork" (initial, title, tint), as web does.
test('a title without a poster draws its generated cover at the card size', async () => {
  const react = await import('react');
  const {generatedPoster} = await screenModule(new URL('../src-next/screens/home/generated-poster.tsx', import.meta.url), {
    react, '@portico/design': {generatedCoverTint: (title: string) => ({first: `first:${title}`, second: 'second'})},
    '../../kit/artwork': {GeneratedCover: 'GeneratedCover'},
  });
  const cover = generatedPoster('  Ash Lane', 200) as {type: unknown; props: Record<string, unknown>};
  assert.equal(cover.type, 'GeneratedCover');
  assert.equal(cover.props.title, '  Ash Lane');
  assert.equal(cover.props.glyph, 'A');
  assert.deepEqual([cover.props.width, cover.props.height], [200, 300]);
  assert.deepEqual(cover.props.tint, {first: 'first:  Ash Lane', second: 'second'});
});
