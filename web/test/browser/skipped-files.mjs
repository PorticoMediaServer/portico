// Isolated component verification; test ports are never registered in the app.
import assert from 'node:assert/strict';
import {writeFile, rm} from 'node:fs/promises';
import {createServer} from 'vite';
import {chromium} from 'playwright';
const entry = '.skipped-files-test.tsx';
const source = `import React from 'react';
import {createRoot} from 'react-dom/client';
import {SkippedFiles} from './src/screens/settings/libraries/SkippedFiles';
import {IconSprite} from './src/kit/Icon';
import './src/kit/tokens.css';
const service = {selectLibrary: async () => {}, getSnapshot: () => ({selected: {data: {library: {id: 'test-library', name: 'TV Shows', lastScan: null}}}})};
createRoot(document.getElementById('root')).render(<><IconSprite/><SkippedFiles libraryId="test-library" api={{}} service={service} isCurrent={() => true} label="See the list"/></>);`;
let server, browser;
try {
  await writeFile(entry, source);
  server = await createServer({server: {port: 0, strictPort: false}});
  server.middlewares.use('/__skipped_test', async (_req, res) => {
    res.setHeader('Content-Type', 'text/html');
    res.end(await server.transformIndexHtml('/__skipped_test', '<html><body><div id="root"></div><script type="module" src="/'+entry+'"></script></body></html>'));
  });
  await server.listen();
  browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || '/snap/bin/chromium', args: ['--no-sandbox']});
  const page = await browser.newPage({viewport: {width: 1440, height: 900}});
  await page.goto('http://127.0.0.1:'+server.httpServer.address().port+'/__skipped_test');
  await page.getByRole('button', {name: 'See the list', exact: true}).click();
  const dialog = page.getByRole('dialog', {name: 'Files skipped', exact: true});
  await dialog.getByText('No files skipped', {exact: true}).waitFor();
  assert.equal(await dialog.getByText('Every file in TV Shows can be played. Titles Portico couldn’t match are in the library under their file names.', {exact: true}).count(), 1);
  assert.equal(await dialog.getByRole('button', {name: 'Scan again', exact: true}).count(), 1);
  assert.equal(await dialog.getByText('0 files', {exact: true}).count(), 0);
  assert.equal(await dialog.locator('ul').count(), 0);
  if (process.env.PORTICO_RUNNER_OUTPUT) await page.screenshot({path: process.env.PORTICO_RUNNER_OUTPUT+'/skipped-empty.jpg', type: 'jpeg', quality: 85});
  await dialog.getByRole('button', {name: 'Close', exact: true}).last().click();
  await page.getByRole('button', {name: 'See the list', exact: true}).waitFor();
  console.log('Skipped files empty state: pass');
} finally {
  await browser?.close();
  await server?.close();
  await rm(entry, {force: true});
}
