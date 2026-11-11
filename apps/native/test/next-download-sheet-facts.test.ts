import test from 'node:test';
import assert from 'node:assert/strict';
import {downloadSheetMeaning} from '../src-next/screens/downloads/download-sheet-facts.ts';

test('the sheet says the original is the file as it is and a made copy is smaller, and says nothing of a choice that cannot be had', () => {
  assert.equal(downloadSheetMeaning({available: true, quality: 'original', prepared: false}), 'downloads.quality.originalHelp');
  assert.equal(downloadSheetMeaning({available: true, quality: '720p-low', prepared: true}), 'downloads.quality.smaller');
  assert.equal(downloadSheetMeaning({available: false, quality: '720p-low', prepared: true}), undefined);
});
