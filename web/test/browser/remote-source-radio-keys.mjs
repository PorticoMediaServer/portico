// Mounted keyboard regression for WN-10. Requires the repository's local Vite,
// React, Radix and Playwright/Chromium dependencies. Authored but UNRUN in the
// work-package sandbox, which omits those app dependencies and the browser.
import assert from 'node:assert/strict';
import {mkdir, mkdtemp, writeFile, rm} from 'node:fs/promises';
import {createServer} from 'vite';
import {chromium} from 'playwright';

await mkdir('.scratch', {recursive: true});
const scratch = await mkdtemp('.scratch/remote-radio-keys-'), entry = `${scratch}/entry.tsx`;
let server, browser;
try {
  await writeFile(entry, `import React, {useState} from 'react';
import {createRoot} from 'react-dom/client';
import {defaultI18n} from '@i18n';
import {RemoteSourceService, sourceReadPreset} from '@core/remote-sources.ts';
import {parseSourceChoiceDefinitions, sourceChoiceNames} from '@core/remote-source-choice.ts';
import {DriveModePane} from '/src/screens/settings/remote-sources/DrivePanes';
import {OfferedReadingChoices} from '/src/screens/settings/remote-sources/OfferedReadingChoices';
import {ReadingChoices} from '/src/screens/settings/remote-sources/ReadingChoices';
import {SourceAdvancedRows} from '/src/screens/settings/remote-sources/SourceAdvancedRows';
import {AddSourceDialog} from '/src/screens/settings/remote-sources/RemoteSourcesPage';
import '/src/kit/tokens.css';
import '/src/bootstrap.css';
const advanced = Object.freeze({scanIntervalSeconds:21600,watchForChanges:false,scanOnStart:false,fullRecheckDays:0,
  cacheBytes:0,cacheFloorBytes:0,seekPreviews:true,chapterImages:true,waveforms:true,loudness:true,checksums:true,
  introsMode:'inherit',doubleCheckFolders:false,dirCacheSeconds:0,readBufferBytes:0,connectTimeoutSeconds:0,idleTimeoutSeconds:0,retries:0,lowLevelRetries:0});
const definitions = parseSourceChoiceDefinitions(sourceChoiceNames.map(name => ({name,definitionRevision:1,reading:sourceReadPreset(name),advanced})))!;
const events = [], requests = [];
const api = {request: async (...args) => {requests.push(args);throw new Error('The keyboard fixture must not make an API request.');}};
const service = new RemoteSourceService(api, {serverId:'server',viewerId:'owner'}, async () => '00000000-0000-4000-8000-000000000001', () => true);
const current = () => true, reportError = error => {throw error;}, onClose = () => {}, onCreated = () => {};
function Proof({screen,disabled}) {
  const [mode,setMode] = useState('service'), [selected,setSelected] = useState(definitions[1]);
  const [values,setValues] = useState(sourceReadPreset('once')), [analysis,setAnalysis] = useState(advanced);
  return <><button id="before">Before</button><main id="proof" data-screen={screen}>
    {screen === 'drive' && <DriveModePane mode={mode} disabled={disabled} onChange={value => {events.push(value);setMode(value);}}/>}
    {screen === 'offered' && <OfferedReadingChoices definitions={definitions} selected={selected} disabled={disabled}
      onSelect={value => {events.push({name:value.name,exact:definitions.includes(value)});setSelected(value);}}/>}
    {screen === 'reading' && <ReadingChoices values={values} rangeSupport="unsupported" disabled={disabled} onChange={value => {events.push(value);setValues(value);}}/>}
    {screen === 'advanced' && <SourceAdvancedRows values={analysis} cacheSupported={false} transportSupported={false} cacheApplied={false}
      observedInterval={21600} disabled={disabled} onChange={value => {events.push(value);setAnalysis(value);}}/>}
    {screen === 'wizard' && <AddSourceDialog service={service} api={api} serverId="server" viewerId="owner" open
      isCurrent={current} reportError={reportError} onClose={onClose} onCreated={onCreated}/>}
  </main><button id="after">After</button></>;
}
const root = createRoot(document.getElementById('root')!);let opening=0;
const show = (screen,disabled=false) => {events.length=0;root.render(<Proof key={++opening} screen={screen} disabled={disabled}/>);};
Object.assign(window,{probe:{show,events,requests,continueLabel:defaultI18n.t('next.remoteSources.continue')}});
show('drive');
`);
  server = await createServer({server: {host: '127.0.0.1', port: 0, strictPort: false}});
  server.middlewares.use('/__remote_radio_keys', async (_request, response) => {
    response.setHeader('Content-Type', 'text/html');
    response.end(await server.transformIndexHtml('/__remote_radio_keys', `<html><body><div id="root"></div><script type="module" src="/${entry}"></script></body></html>`));
  });
  await server.listen();
  browser = await chromium.launch({headless: true, ...(process.env.CHROMIUM_PATH ? {executablePath: process.env.CHROMIUM_PATH} : {})});
  const page = await browser.newPage({viewport: {width: 1280, height: 1000}}), errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__remote_radio_keys`);
  const checked = async (group, index) => {
    await group.getByRole('radio').nth(index).locator('xpath=self::*[@aria-checked="true"]').waitFor();
    assert.equal(await group.getByRole('radio').nth(index).evaluate(node => node === document.activeElement), true);
    assert.equal(await group.getByRole('radio').evaluateAll(nodes => nodes.filter(node => node.tabIndex === 0).length), 1);
  };
  const show = async (screen, disabled = false) => {
    await page.evaluate(({screen, disabled}) => window.probe.show(screen, disabled), {screen, disabled});
    await page.locator(`#proof[data-screen="${screen}"]`).waitFor();
  };

  // One Tab stop; arrows update the real controlled value, Home/End work, and
  // Tab exits the group instead of visiting every sign-in card.
  const drive = page.locator('#proof').getByRole('radiogroup');
  await page.locator('#before').focus();await page.keyboard.press('Tab');await checked(drive, 0);
  await page.keyboard.press('ArrowDown');await checked(drive, 1);
  await page.keyboard.press('Home');await checked(drive, 0);
  await page.keyboard.press('End');await checked(drive, 1);
  await page.keyboard.press('Tab');assert.equal(await page.locator('#after').evaluate(node => node === document.activeElement), true);
  assert.deepEqual(await page.evaluate(() => window.probe.events), ['oauth', 'service', 'oauth']);

  // The server's exact definition object, not a same-name reconstructed preset,
  // reaches the existing callback on keyboard selection.
  await show('offered');
  const offered = page.locator('#proof').getByRole('radiogroup');
  await page.locator('#before').focus();await page.keyboard.press('Tab');await checked(offered, 1);
  await page.keyboard.press('ArrowDown');await checked(offered, 2);
  assert.deepEqual(await page.evaluate(() => window.probe.events), [{name: 'everything', exact: true}]);

  // An unavailable option is skipped, and a wholly disabled group has no Tab stop.
  await show('reading');
  const reading = page.locator('#proof').getByRole('radiogroup');
  assert.equal(await reading.getByRole('radio').nth(2).isDisabled(), true);
  await page.locator('#before').focus();await page.keyboard.press('Tab');await checked(reading, 1);
  await page.keyboard.press('ArrowDown');await checked(reading, 3);
  await show('drive', true);
  assert.equal(await page.locator('#proof').getByRole('radio').evaluateAll(nodes => nodes.every(node => node.disabled)), true);
  await page.locator('#before').focus();await page.keyboard.press('Tab');
  assert.equal(await page.locator('#after').evaluate(node => node === document.activeElement), true);
  assert.deepEqual(await page.evaluate(() => window.probe.events), []);

  // Each advanced Run/Skip pair is independent. A move changes its own field;
  // the next row's checked state and single Tab stop are preserved.
  await show('advanced');
  const groups = page.locator('#proof').getByRole('radiogroup');
  assert.equal(await groups.count(), 5);
  await groups.nth(0).getByRole('radio').nth(0).focus();
  await page.keyboard.press('ArrowRight');await checked(groups.nth(0), 1);
  assert.equal(await groups.nth(1).getByRole('radio').nth(0).getAttribute('aria-checked'), 'true');
  await page.keyboard.press('Tab');await checked(groups.nth(1), 0);
  assert.equal(await page.evaluate(() => window.probe.events.at(-1).seekPreviews), false);
  assert.equal(await page.evaluate(() => window.probe.events.at(-1).chapterImages), true);

  // The real wizard retains its original guards. Source kinds are one group
  // across their visual section headings, so a keyboard user reaches Drive.
  await show('wizard');
  const dialog = page.getByRole('dialog'), families = dialog.getByRole('radiogroup');
  await families.getByRole('radio').nth(0).focus();await page.keyboard.press('ArrowDown');await checked(families, 1);
  await dialog.getByRole('button', {name: await page.evaluate(() => window.probe.continueLabel), exact: true}).click();
  const kinds = dialog.getByRole('radiogroup').first();
  await kinds.getByRole('radio').nth(0).focus();await page.keyboard.press('ArrowDown');await checked(kinds, 1);
  assert.equal(await kinds.getByRole('radio').count(), 4);
  assert.deepEqual(await page.evaluate(() => window.probe.requests), []);
  assert.deepEqual(errors, []);
  console.log('PASS remote-source radio Tab/arrows/Home/End, disabled skipping, exact definitions, independent advanced rows and wizard kind traversal');
} finally {
  await browser?.close();
  await server?.close();
  await rm(scratch, {recursive: true, force: true});
}
