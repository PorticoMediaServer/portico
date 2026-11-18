import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {recordingOptionsKey} from '../src/screens/live/ProgrammePanel.tsx';

test('a booked airing that has not started is named as one that will record, not as recording now', () => {
  assert.equal(defaultI18n.t(recordingOptionsKey(true)), 'Will record. Open recording options');
  assert.equal(defaultI18n.t(recordingOptionsKey(false)), 'Recording now. Open recording options');
});
