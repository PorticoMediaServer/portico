// Authored guard vectors for the real component; not a visual/runtime acceptance.
import assert from 'node:assert/strict';
import {mkdir, mkdtemp, rm, writeFile} from 'node:fs/promises';
import {createServer} from 'vite';
import {chromium} from 'playwright';
await mkdir('.scratch', {recursive: true});
const scratch = await mkdtemp('.scratch/invitations-guards-'), entry = scratch + '/entry.tsx';
const source = `import React from 'react';
import {createRoot} from 'react-dom/client';
import {PeopleInvitations} from '/src/screens/settings/PeopleInvitations';
const root = createRoot(document.getElementById('root'));
const revokes = [], links = [], reads = [], clipboard = [];
let reports = 0, version = 0;
const row = {id:'invitation', revision:7, state:'pending', role:'member', notes:'Resident invitation', allowedLibraries:[], createdAt:'2026-10-23T12:00:00Z', expiresAt:new Date(Date.now()+3600000).toISOString(), access:{maxStreamsMode:'inherit', maxStreams:0, allowRemote:true, accessTokens:true, maxContentRating:''}};
const authority = {isCurrent:()=>true,onAuthorityChanged:()=>{}};
const invitations = {
 read:async(query,cursor,life)=>{reads.push({query,cursor,life});return {items:[row,{...row,id:'producer-pending',expiresAt:'2000-01-01T00:00:00Z'}],nextCursor:'',matchedCount:73,totalCount:101};},
 link:(row,life)=>new Promise((resolve,reject)=>links.push({row,life,resolve,reject})),
 revoke:(row,operationId,life)=>new Promise((resolve,reject)=>revokes.push({row,operationId,life,resolve,reject}))
};
Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{clipboard.push(text);}}});
let originalService = invitations, originalAuthority = authority;
const render=()=>root.render(<React.StrictMode><PeopleInvitations invitations={originalService} serverName="Demo" signIn="local" libraries={[]} authority={originalAuthority} version={version} openHome={()=>{}} reportError={()=>{reports++;}}/></React.StrictMode>);
window.probe={revokes,links,reads,clipboard,get reports(){return reports;},replace:()=>{originalService={...invitations};originalAuthority={...authority};render();},refresh:()=>{version++;render();},unmount:()=>root.render(null)};
render();`;
let server, browser;
try {
 await writeFile(entry, source);
 server = await createServer({server: {port: 0, strictPort: false}});
 server.middlewares.use('/__invitations_guards', async (_req, res) => {
  res.setHeader('Content-Type', 'text/html');
  res.end(await server.transformIndexHtml('/__invitations_guards', '<html><body><div id="root"></div><script type="module" src="/' + entry + '"></script></body></html>'));
 });
 await server.listen();
 browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || '/snap/bin/chromium', args: ['--no-sandbox'], chromiumSandbox: false});
 const page = await browser.newPage({viewport: {width: 1440, height: 900}});
 await page.goto('http://127.0.0.1:' + server.httpServer.address().port + '/__invitations_guards');
 const copy = page.getByRole('button', {name: 'Copy link', exact: true}).first();
 const rowRevoke = page.getByRole('button', {name: 'Revoke', exact: true}).first();
 const dialog = page.getByRole('dialog');
 const retain = async (locator, key) => locator.evaluate((node, key) => {
  const props = Object.keys(node).find(value => value.startsWith('__reactProps$'));
  window[key] = node[props].onClick;
 }, key);
 await copy.waitFor(); // StrictMode replay must leave a live renewable view.
 assert.equal(await page.getByText('Expired', {exact: true}).count(), 0, 'producer pending is not reclassified by local time');
 // Deferred link: replacing the original query must prevent clipboard use.
 await retain(copy, 'oldCopy');await copy.click();
 await page.waitForFunction(() => probe.links.length === 1);
 await page.getByRole('searchbox').fill('replacement');
 await page.waitForFunction(() => probe.reads.some(value => value.query.search === 'replacement'));
 await page.evaluate(() => {oldCopy({});probe.links[0].resolve('must-not-copy');});
 await copy.waitFor();
 assert.equal(await page.evaluate(() => probe.links.length), 1);
 assert.equal(await page.evaluate(() => probe.clipboard.length), 0);
 // Service/authority replacement must publish one fresh exact layout tuple.
 await retain(copy, 'sourceCopy');
 await page.evaluate(() => probe.replace());
 await copy.waitFor();
 await page.waitForFunction(() => document.querySelector('input[type=search]')?.value === '');
 await page.evaluate(() => sourceCopy({}));
 assert.equal(await page.evaluate(() => probe.links.length), 1, 'old view never borrows the successor service');
 // Cancel A synchronously consumes it; held Confirm/Cancel A cannot claim B.
 await rowRevoke.click();await dialog.waitFor();
 await retain(dialog.getByRole('button', {name: 'Revoke', exact: true}), 'confirmA');
 await retain(dialog.getByRole('button', {name: 'Cancel', exact: true}), 'cancelA');
 await dialog.getByRole('button', {name: 'Cancel', exact: true}).click();
 await dialog.waitFor({state:'hidden'});
 await rowRevoke.click();await dialog.waitFor();
 await page.evaluate(() => {confirmA({});cancelA({});});
 assert.equal(await page.evaluate(() => probe.revokes.length), 0);
 assert.equal(await dialog.count(), 1);
 await retain(dialog.getByRole('button', {name: 'Revoke', exact: true}), 'confirmB');
 await page.evaluate(() => {confirmB({});confirmB({});});
 await page.waitForFunction(() => probe.revokes.length === 1);
 assert.equal(await page.evaluate(() => probe.revokes.length), 1, 'React batching cannot admit a duplicate');
 await page.evaluate(() => probe.revokes[0].reject({status:503}));
 await page.waitForFunction(() => probe.reports === 1);
 await page.evaluate(() => confirmB({}));
 await page.waitForFunction(() => probe.revokes.length === 2);
 assert.deepEqual(await page.evaluate(() => probe.revokes.slice(0,2).map(value => ({id:value.row.id,revision:value.row.revision,operationId:value.operationId}))),
  await page.evaluate(() => Array(2).fill({id:probe.revokes[0].row.id,revision:probe.revokes[0].row.revision,operationId:probe.revokes[0].operationId})));
 // Version/page replacement retires in-flight B; its settlement cannot refresh
 // the replacement page, clear C's busy state, show a note or report an error.
 await page.evaluate(() => probe.refresh());await copy.waitFor();
 await rowRevoke.click();await dialog.waitFor();
 await dialog.getByRole('button', {name: 'Revoke', exact: true}).click();
 await page.waitForFunction(() => probe.revokes.length === 3);
 const before = await page.evaluate(() => ({reads:probe.reads.length,reports:probe.reports}));
 await page.evaluate(() => probe.revokes[1].resolve({state:'revoked'}));
 await page.waitForTimeout(0);
 assert.deepEqual(await page.evaluate(() => ({reads:probe.reads.length,reports:probe.reports})), before);
 assert.equal(await dialog.count(), 1);
 assert.equal(await dialog.getByRole('button', {name: 'Cancel', exact: true}).isDisabled(), true);
 await page.evaluate(() => probe.unmount());
 await page.evaluate(() => probe.revokes[2].reject({status:503}));
 await page.waitForTimeout(0);
 assert.equal(await page.evaluate(() => probe.reports), before.reports);
} finally {
 await browser?.close();await server?.close();await rm(scratch, {recursive:true,force:true});
}
