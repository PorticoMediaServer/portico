import {readFile, realpath, writeFile} from 'node:fs/promises';
import {basename, dirname, join, resolve, sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {compareScreens} from './compare.mjs';
import {calibrateFrame} from './calibration.mjs';
import {validateMeasurement} from './contract.mjs';
import {assessDifferences} from './assessment.mjs';
import {prepareNativeDeclarations} from './native-declarations.mjs';

/** Native and desktop callers use this same comparator; never duplicate its math. */
export function compareMeasurements(reference, actual, adapter = {}) {
  const invalid = [['reference', reference], ['actual', actual]].flatMap(([side, screen]) => validateMeasurement(screen, {reference: side === 'reference'}).map(path => ({kind: 'invalid-measurement', label: `${side}.${path}`})));
  if (invalid.length) return {scope: reference.scope, verdict: 'FAIL', differenceCount: invalid.length, differences: invalid, assessment: assessDifferences(invalid, {reference, actual})};
  actual = prepareNativeDeclarations(actual);
  const comparison = compareScreens(reference, actual, adapter), calibration = calibrateFrame(reference);
  const differences = [...comparison.differences];
  if (!calibration.passed) differences.push({kind: 'calibration-failure', label: 'Frame self/font/opacity', calibration});
  return {scope: reference.scope, verdict: differences.length ? 'FAIL' : 'PASS', differenceCount: differences.length, differences, comparison, calibration, assessment: assessDifferences(differences, {reference, actual})};
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = Object.fromEntries(process.argv.slice(2).reduce((pairs, value, index, values) => {if (index % 2 === 0) pairs.push([value.replace(/^--/, ''), values[index + 1]]); return pairs;}, []));
  if (!args.reference || !args.actual || !args.output || Object.keys(args).some(key => !['reference', 'actual', 'adapter', 'output'].includes(key))) throw new Error('Expected --reference --actual [--adapter] --output');
  const load = async path => JSON.parse(await readFile(path, 'utf8'));
  const parent = await realpath(dirname(resolve(args.output))), source = resolve(fileURLToPath(new URL('../..', import.meta.url)));
  if (parent === source || parent.startsWith(source + sep)) throw new Error('Reference-derived output must stay outside public source');
  const result = compareMeasurements(await load(args.reference), await load(args.actual), args.adapter ? await load(args.adapter) : {});
  await writeFile(join(parent, basename(args.output)), JSON.stringify(result, null, 2) + '\n', {flag: 'wx', mode: 0o600});
  console.log(JSON.stringify({scope: result.scope, verdict: result.verdict, differenceCount: result.differenceCount, assessment: result.assessment}));
}
