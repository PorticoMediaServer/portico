import test from 'node:test';
import assert from 'node:assert/strict';
import {containerDownloadBytes, downloadSheetMeaning} from '../src-next/screens/downloads/download-sheet-facts.ts';

test('a film row explains the actual source or a genuinely smaller offered copy', () => {
  const source = {available: true, bytes: 1000};
  assert.equal(downloadSheetMeaning({available: true, kind: 'source', estimatedBytes: 1000}, source), 'downloads.quality.originalHelp');
  assert.equal(downloadSheetMeaning({available: true, kind: 'optimized', estimatedBytes: 600}, source), 'downloads.quality.smaller');
  assert.equal(downloadSheetMeaning({available: false, kind: 'optimized', estimatedBytes: 600}, source), undefined, 'an unavailable row keeps its actual refusal instead');
});

test('source equivalence wins over a smaller estimate and unknown/equal/larger sizes invent no smaller meaning', () => {
  const offered = {available: true, kind: 'optimized' as const, estimatedBytes: 600};
  assert.equal(downloadSheetMeaning({...offered, sameAsOriginal: true}, {available: true, bytes: 1000}), 'quality.sameAsOriginal');
  assert.equal(downloadSheetMeaning(offered, {available: false, bytes: 1000}), undefined);
  assert.equal(downloadSheetMeaning(offered, {available: true, bytes: 0}), undefined);
  assert.equal(downloadSheetMeaning({...offered, estimatedBytes: 0}, {available: true, bytes: 1000}), undefined);
  assert.equal(downloadSheetMeaning({...offered, estimatedBytes: 1000}, {available: true, bytes: 1000}), undefined);
  assert.equal(downloadSheetMeaning({...offered, estimatedBytes: 1200}, {available: true, bytes: 1000}), undefined);
});

test('a season or album with unknown member sizes never displays its partial byte sum as the whole size', () => {
  assert.equal(containerDownloadBytes({unknownSizeCount: 1, estimatedBytes: 600}), undefined);
  assert.equal(containerDownloadBytes({unknownSizeCount: 3, estimatedBytes: 0}), undefined);
  assert.equal(containerDownloadBytes({unknownSizeCount: 0, estimatedBytes: 600}), 600);
  assert.equal(containerDownloadBytes({unknownSizeCount: 0, estimatedBytes: 0}), 0, 'a known zero stays distinct from an unknown size');
});
