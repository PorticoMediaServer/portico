import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {join} from 'node:path';
import {decodeGetAdminDiagnosticsConcurrencyResponse} from '../../src/decoders.ts';
import {schemas} from '../../src/schemas.ts';

// Final batch supplies the fresh directory populated by the real Go owner
// producer. Unlimited and unrepresentable finite limits arrive as null, while a
// representable limit keeps its exact integer.
const directory = process.env.PORTICO_CONCURRENCY_DIAGNOSTICS_FIXTURE_DIR;
assert.ok(directory, 'record the real concurrency diagnostics responses before running this final-batch test');
const load = (file: string) => JSON.parse(readFileSync(join(directory, file), 'utf8')).response as any;

test('concurrency diagnostics publish memory limits without unsafe numbers', () => {
 const unlimited = decodeGetAdminDiagnosticsConcurrencyResponse(load('get-admin-diagnostics-concurrency-unlimited.json')) as any;
 assert.equal(unlimited.process.memoryLimitBytes, null);
 assert.equal('memoryLimitBytesExact' in unlimited.process, false);

 const finite = decodeGetAdminDiagnosticsConcurrencyResponse(load('get-admin-diagnostics-concurrency-finite.json')) as any;
 assert.equal(finite.process.memoryLimitBytes, 1073741824);
 assert.equal('memoryLimitBytesExact' in finite.process, false);

 const unsafe = load('get-admin-diagnostics-concurrency-unsafe-finite.json');
 const decoded = decodeGetAdminDiagnosticsConcurrencyResponse(unsafe) as any;
 assert.equal(decoded.process.memoryLimitBytes, null);
 assert.equal(decoded.process.memoryLimitBytesExact, '1152921504606846976');

 // The integer is required and cannot carry the unsafe finite value itself.
 const missing = structuredClone(unsafe);
 delete missing.process.memoryLimitBytes;
 assert.throws(() => decodeGetAdminDiagnosticsConcurrencyResponse(missing));
 for (const value of [1152921504606846976, 9223372036854775807, -1]) {
  assert.throws(() => decodeGetAdminDiagnosticsConcurrencyResponse({...unsafe, process: {...unsafe.process, memoryLimitBytes: value}}));
 }

 // The optional companion is canonical positive decimal text only.
 for (const value of ['0', '01', '-5', '1e3', ' 12']) {
  assert.throws(() => decodeGetAdminDiagnosticsConcurrencyResponse({...unsafe, process: {...unsafe.process, memoryLimitBytesExact: value}}));
 }

 const processSchema = ((schemas.GetAdminDiagnosticsConcurrencyResponse as any).properties.process) as any;
 assert.equal(processSchema.properties.memoryLimitBytes.maximum, 9007199254740991);
 assert.equal(processSchema.properties.memoryLimitBytes.nullable, true);
 assert.equal(processSchema.properties.memoryLimitBytesExact.pattern, '^[1-9][0-9]*$');
 assert.ok(processSchema.required.includes('memoryLimitBytes'));
 assert.equal(processSchema.required.includes('memoryLimitBytesExact'), false);
});
