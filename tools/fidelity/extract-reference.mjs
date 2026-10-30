import {readFile, realpath, writeFile} from 'node:fs/promises';
import {basename, dirname, join, resolve, sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {withMeasurementBrowser} from './browser.mjs';
import {authoredReferenceScreen} from './reference-screen.mjs';
import {fontReadiness} from './readiness.mjs';
import {validateMeasurement} from './contract.mjs';
import {componentMeasurement} from './component.mjs';
import {settleMotion} from './gallery-entry.mjs';

/** Read a verified complete authored viewport without moving or rewriting its DOM. */
export async function extractReference({url, selector, windowViewport, viewport, anchors = {}, repeats = [], resolvedFonts = false, component = false, motion, motionTimeoutMs}) {
  return withMeasurementBrowser({scratch: process.env.TMPDIR, executable: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
    viewport: windowViewport, allowedOrigins: [new URL(url).origin, 'https://fonts.googleapis.com', 'https://fonts.gstatic.com']}, async browser => {
    await browser.navigate(url, selector);
    if (motion !== undefined && motion !== 'settled') throw new Error('Unknown reference motion state');
    if (motion === 'settled') await settleMotion(browser, {selector, timeoutMs: motionTimeoutMs ?? 15000});
    if (component) {
      const natural = await browser.evaluate(`(()=>{let node=document.querySelector(${JSON.stringify(selector)});for(;node;node=node.parentElement){const style=getComputedStyle(node);if(Number(style.zoom)!==1||style.transform!=='none'&&style.transform.replaceAll(' ','')!=='matrix(1,0,0,1,0,0)')return false;}return true;})()`);
      if (!natural) throw new Error('Reference component must be in its natural untransformed layout');
    }
    const measure = async () => {const screen = await browser.measure(selector, anchors, repeats); return resolvedFonts ? browser.observeRenderedFonts(screen, selector) : screen;};
    const first = await measure(), second = await measure();
    if (JSON.stringify(first) !== JSON.stringify(second)) throw new Error('Reference extraction is nondeterministic');
    const readiness = {blocked: browser.blocked, pageErrors: browser.pageErrors, fonts: fontReadiness(first, 'reference')};
    if (readiness.blocked.length || readiness.pageErrors.length || readiness.fonts.length) throw new Error('Reference readiness: ' + JSON.stringify(readiness));
    const screen = component ? componentMeasurement(first, {reference: true}) : authoredReferenceScreen(first, viewport), errors = validateMeasurement(screen, {reference: true});
    if (errors.length) throw new Error('Invalid reference measurement: ' + errors.join(', '));
    return screen;
  });
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = Object.fromEntries(process.argv.slice(2).reduce((pairs, value, index, values) => {if (index % 2 === 0) pairs.push([value.replace(/^--/, ''), values[index + 1]]); return pairs;}, []));
  if (!args.url || !args.adapter || !args.output || Object.keys(args).some(key => !['url', 'adapter', 'output'].includes(key))) throw new Error('Expected --url --adapter --output');
  const parent = await realpath(dirname(resolve(args.output))), source = resolve(fileURLToPath(new URL('../..', import.meta.url)));
  if (parent === source || parent.startsWith(source + sep)) throw new Error('Reference output must stay outside public source');
  const adapter = JSON.parse(await readFile(args.adapter, 'utf8')), screen = await extractReference({...adapter, url: args.url});
  await writeFile(join(parent, basename(args.output)), JSON.stringify(screen, null, 2) + '\n', {flag: 'wx', mode: 0o600});
  console.log(JSON.stringify({scope: screen.scope, elements: screen.elements.length, viewport: screen.viewport}));
}
