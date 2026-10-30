import {resolve, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {readFile, writeFile, realpath} from 'node:fs/promises';
import {basename, dirname, sep} from 'node:path';
import {createServer} from 'vite';
import {withMeasurementBrowser} from './browser.mjs';
import {enUS} from '../../packages/i18n/src/catalog/en-US.ts';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {compareCatalogueStrings} from './strings.mjs';
import {compareTextMetrics} from './text-metrics.mjs';
import {compareScreens} from './compare.mjs';
import {auth4} from './auth4.mjs';
import {applyControlledState, checkControlledState} from './controlled-state.mjs';
import {calibrateFrame} from './calibration.mjs';
import {fontReadiness} from './readiness.mjs';
import {controlledEntry, verifyGalleryEntry, settleMotion} from './gallery-entry.mjs';
import {extractReference} from './extract-reference.mjs';
import {assessDifferences} from './assessment.mjs';
import {componentMeasurement} from './component.mjs';

const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));

/** Full declared frame versus the actual DEV screen, with measured state and no API. */
export async function compareWebScreen({referenceURL, adapter = auth4}) {
  const {referenceSelector, entry, actualSelector, viewport} = adapter;
  if (!process.env.TMPDIR || !process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) throw new Error('Owned runner scratch and explicit Chromium required');
  controlledEntry(entry);
  // Contact-sheet frames are measured at their natural size in a large enough
  // window. The shared extractor rejects scaled roots; no DOM/CSS is rewritten.
  const authoredReference = adapter.referenceWindowViewport ? await extractReference({url: referenceURL,
    selector: referenceSelector, windowViewport: adapter.referenceWindowViewport, viewport,
    anchors: adapter.reference, repeats: adapter.repeats?.reference, component: adapter.component === true,
    motion: adapter.referenceMotion, motionTimeoutMs: adapter.referenceMotionTimeoutMs}) : undefined;
  const vite = await createServer({root: join(root, 'web'), configFile: join(root, 'web/vite.config.ts'),
    cacheDir: join(process.env.TMPDIR, 'fidelity-vite'), server: {host: '127.0.0.1', port: 0, strictPort: false}});
  try {
    await vite.listen();
    const actualOrigin = `http://127.0.0.1:${vite.httpServer.address().port}`, actualURL = actualOrigin + entry;
    return await withMeasurementBrowser({scratch: process.env.TMPDIR, executable: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
      viewport, allowedOrigins: [new URL(referenceURL).origin, actualOrigin, 'https://fonts.googleapis.com', 'https://fonts.gstatic.com']}, async browser => {
      if (!authoredReference) {
        await browser.navigate(referenceURL, referenceSelector);
        if (adapter.referenceMotion === 'settled') await settleMotion(browser, {selector: referenceSelector, timeoutMs: adapter.referenceMotionTimeoutMs ?? 15000});
      }
      const reference = authoredReference ?? await browser.measure(referenceSelector, adapter.reference, adapter.repeats?.reference);
      const deterministicReference = !!authoredReference || JSON.stringify(reference) === JSON.stringify(await browser.measure(referenceSelector, adapter.reference, adapter.repeats?.reference));
      await browser.navigate(actualURL, adapter.readySelector ?? actualSelector);
      await verifyGalleryEntry(browser, entry, viewport);
      await applyControlledState(browser, adapter.stateSteps);
      if (adapter.motion === 'settled') await settleMotion(browser, {selector: actualSelector});
      const measureActual = async () => {
        const measured = await browser.measure(actualSelector, adapter.actual, adapter.repeats?.actual);
        return adapter.component ? componentMeasurement(measured) : measured;
      };
      const actual = await measureActual();
      const deterministicActual = JSON.stringify(actual) === JSON.stringify(await measureActual());
      const comparison = compareScreens(reference, actual, adapter), strings = compareCatalogueStrings(reference, actual, enUS, adapter, comparison.cascadeRoots, (key, values) => defaultI18n.t(key, values)), textMetrics = compareTextMetrics(reference, actual);
      const differences = [...comparison.differences, ...strings.differences];
      const calibration = calibrateFrame(reference);
      if (!calibration.passed) differences.push({kind: 'calibration-failure', label: 'Frame self and single-fact perturbations', calibration});
      differences.push(...fontReadiness(reference, 'reference'), ...fontReadiness(actual, 'actual'));
      const controlled = checkControlledState(actual, adapter.expectations);
      differences.push(...controlled.differences);
      if (!deterministicReference || !deterministicActual) differences.push({kind: 'nondeterministic-extraction', label: 'Same-input determinism', reference: deterministicReference, actual: deterministicActual});
      if (browser.blocked.length || browser.pageErrors.length) differences.push({kind: 'page-readiness', label: 'Blocked or failed runtime resource', blocked: browser.blocked, pageErrors: browser.pageErrors});
      const nativeCarets = controlled.carets, nativeCaret = nativeCarets[0] ?? null;
      if (nativeCarets.length) comparison.diagnostics.push({label: 'Native input caret illustration', scored: false, ruling: 27, properties: ['x', 'width', 'blink'], reason: 'Binding native-input illustration erratum; no fake caret or input metric compensation.'});
      const classification = {adapter: 0, stateOrReadiness: 0, copyOrCatalogue: 0, geometry: 0, computedStyle: 0, unmatchedElement: 0};
      for (const difference of differences) {
        if (['anchor-cardinality', 'list-cardinality', 'ambiguous-match', 'missing-content-slot', 'message-slot-invalid'].includes(difference.kind)) classification.adapter++;
        else if (['screen-state', 'fixture-state', 'nondeterministic-extraction', 'font-not-loaded', 'page-readiness', 'native-caret-state', 'calibration-failure'].includes(difference.kind)) classification.stateOrReadiness++;
        else if (difference.kind.includes('string') || difference.property === 'text') classification.copyOrCatalogue++;
        else if (difference.property?.startsWith('box.')) classification.geometry++;
        else if (difference.kind === 'measured-difference') classification.computedStyle++;
        else classification.unmatchedElement++;
      }
      return {version: 1, id: adapter.id, state: adapter.state, scope: adapter.component ? 'component' : 'screen', viewport, reference, actual,
        comparison, strings, textMetrics, nativeCaret, nativeCarets, calibration, differences, differenceCount: differences.length, classification,
        assessment: assessDifferences(differences, {reference, actual}),
        deterministic: {reference: deterministicReference, actual: deterministicActual},
        network: {blocked: browser.blocked}, pageErrors: browser.pageErrors,
        verdict: differences.length ? 'FAIL' : 'PASS', qualification: adapter.component ? 'Genuine measured component subtree; never full-screen or application acceptance. Artwork contents excluded; no screenshot files or private source URLs.' : 'Controlled genuine full-screen state, not a complete authentication flow. Artwork contents excluded; image slot geometry/style included. No screenshot files or private source URLs.'};
    });
  } finally {await vite.close();}
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const argv = process.argv.slice(2), args = {};
  for (let index = 0; index < argv.length; index += 2) {
    if (!argv[index].startsWith('--') || argv[index + 1] === undefined) throw new Error('Named argument/value pairs required');
    const name = argv[index].slice(2); if (name in args) throw new Error('Duplicate argument'); args[name] = argv[index + 1];
  }
  if (!args['reference-url'] || !args.output || Object.keys(args).some(key => !['reference-url', 'output', 'adapter'].includes(key))) throw new Error('Reference input and external output required');
  const output = resolve(args.output), parent = await realpath(dirname(output));
  if (parent === root || parent.startsWith(root + sep)) throw new Error('Reference-derived output must stay outside public source');
  const adapter = args.adapter ? JSON.parse(await readFile(args.adapter, 'utf8')) : auth4;
  const result = await compareWebScreen({referenceURL: args['reference-url'], adapter});
  await writeFile(join(parent, basename(output)), JSON.stringify(result, null, 2) + '\n', {flag: 'wx', mode: 0o600});
  console.log('FIDELITY_RESULT ' + JSON.stringify({id: result.id, state: result.state, scope: result.scope, verdict: result.verdict, differenceCount: result.differenceCount, classification: result.classification, assessment: result.assessment}));
}
