import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { decode } from '../src/runtime.ts';
import { schemas } from '../src/schemas.ts';
const directory = new URL('../../../server/testdata/contracts/', import.meta.url);
const files = readdirSync(directory).filter(file => file.endsWith('.json'));
assert.ok(files.length > 0, 'No handler contract fixtures recorded');
for (const file of files) test(file, () => {
  const fixture = JSON.parse(readFileSync(new URL(file, directory), 'utf8'));
  assert.ok(fixture.schema in schemas);
  assert.doesNotThrow(() => decode(fixture.schema, fixture.response));
  // The accepted start resource is deliberately closed by closeStartObjects;
  // other recorded response contracts retain their open forward-compatible shape.
  if (fixture.schema === 'StartPlaybackSessionResponse') {
    assert.throws(() => decode(fixture.schema, {...fixture.response, futureField: true}));
  } else {
    assert.doesNotThrow(() => decode(fixture.schema, {...fixture.response, futureField: true}));
  }
  assert.throws(() => decode(fixture.schema, null));
});
