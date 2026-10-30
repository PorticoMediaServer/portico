import test from 'node:test';
import assert from 'node:assert/strict';
import {schemas} from '../src/schemas.ts';

test('DVR recording, overlap, group and rule channel contracts carry only optional canonical marks', () => {
  let channels = 0;
  function visit(schema: unknown): void {
    if (!schema || typeof schema !== 'object') return;
    if (Array.isArray(schema)) {schema.forEach(visit); return;}
    const node = schema as Record<string, any>;
    const channel = node.properties?.channel;
    if (channel?.properties?.id && channel.properties.name && channel.properties.number) {
      channels++;
      assert.equal(channel.required.includes('mark'), false);
      const mark = channel.properties.mark;
      assert.ok(mark, 'a DVR channel lost its actual optional mark');
      assert.equal(mark.type, 'object');
      assert.notEqual(mark.nullable, true);
      assert.equal(mark.anyOf, undefined);
      assert.equal(mark.oneOf, undefined);
      assert.deepEqual(Object.keys(mark.properties).sort(), ['seed', 'text']);
      assert.deepEqual([...mark.required].sort(), ['seed', 'text']);
      assert.equal(mark.properties.seed.type, 'string');
      assert.equal(mark.properties.text.type, 'string');
    }
    Object.values(node).forEach(visit);
  }
  for (const name of ['GetDvrResponse', 'GetDvrGroupsResponse', 'GetDvrRecordingsIdResponse', 'PutDvrRulesIdResponse'] as const) {
    const before = channels;
    visit(schemas[name]);
    assert.ok(channels > before, `${name} has no channel projection`);
  }
  assert.ok(channels >= 6, 'recording, overlap and rule-summary projections must all be checked');
});
