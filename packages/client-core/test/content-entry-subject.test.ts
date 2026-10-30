import test from 'node:test';
import assert from 'node:assert/strict';
import {contentEntrySubject} from '../src/index.ts';
import {contentEntrySubjectVectors} from './fixtures/content-entry-subject.ts';

for (const vector of contentEntrySubjectVectors) test(vector.name, () => {
  assert.deepEqual(contentEntrySubject(vector.entry), vector.subject);
});
