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
  // Responses decode open at every depth: an unknown member is dropped, never rejected.
  const extended = decode(fixture.schema, {...fixture.response, futureField: true}) as Record<string, unknown>;
  assert.equal('futureField' in extended, false);
  assert.throws(() => decode(fixture.schema, null));
});
