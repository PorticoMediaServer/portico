import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {join} from 'node:path';
import {validateContentEntry} from '../../src/library-content.ts';

// Final batch supplies the directory populated by the real Go HTTP producer.
test('Reading saved Home responses use canonical content entries', () => {
  const directory = process.env.PORTICO_READING_HOME_FIXTURE_DIR;
  assert.ok(directory, 'record Go HTTP responses before running this final-batch test');
  for (const [library, kind] of [['book', 'ebook'], ['comic', 'comic']] as const) {
    const response = JSON.parse(readFileSync(join(directory, library + '.json'), 'utf8'));
    assert.equal(response.entries.length, 2);
    for (const raw of response.entries) {
      const entry = validateContentEntry(raw);
      assert.equal(entry.kind, kind);
      assert.equal(entry.playback, undefined);
      assert.equal(entry.duration, undefined);
    }
  }
});
