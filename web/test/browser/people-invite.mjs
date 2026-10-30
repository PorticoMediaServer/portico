// Runs the real People workspace in Chromium against deferred invitation ports.
import assert from 'node:assert/strict';
import {writeFile, rm} from 'node:fs/promises';
import {createServer} from 'vite';
import {chromium} from 'playwright';
const entry = '.people-invite-workspace-test.tsx';
const source = `import React from 'react';
import {createRoot} from 'react-dom/client';
import {PeopleScreen} from './src/screens/settings/People';
const calls = [], waiting = [];
let reports = 0, retirements = 0, issues = 0, current = true;
const bindings = {
 client: {read: async () => ({items: [], totalCount: 0, matchedCount: 0, nextCursor: '', limit: 40}), dispose() {}},
 serverName: 'Test server', libraries: [], actor: {accountId: 'owner', role: 'owner'},
 authority: {scope: {serverId: 'server', authority: 'local', accountId: 'owner', profileId: 'main', sessionFamilyId: 'session'},
 isCurrent: () => current, subscribe: () => () => {}, onAuthorityChanged: () => {retirements++; current = false;}},
 openHome() {}, reportError() {reports++;},
 invitations: {link: async () => 'https://web.getportico.tv/join#code=' + 'a'.repeat(43) + '&server=https%3A%2F%2Fserver.example&id=server', shortCode: async () => {issues++;throw new Error('test issue refusal');}, create: (body) => {calls.push(body);return new Promise((resolve, reject) => waiting.push({resolve, reject}));}}
};
window.probe = {calls, get reports() {return reports;}, get retirements() {return retirements;}, get issues() {return issues;},
 reject: (status) => waiting.shift().reject({status}), resolve: (state) => waiting.shift().resolve({id: 'invite', revision: 1, state, role: 'member', allowedLibraries: [], access:{maxStreamsMode:'inherit', maxStreams:0, allowRemote:true, accessTokens:true, maxContentRating:''}, createdAt: new Date().toISOString(), expiresAt: new Date(Date.now() + 86400000).toISOString(), notes: 'Private invitation note'}),
 mount: () => root.render(<PeopleScreen bindings={bindings}/>), unmount: () => root.render(null)};
const root = createRoot(document.getElementById('root'));
window.probe.mount();`;
let server, browser;
try {
 await writeFile(entry, source);
 server = await createServer({server: {port: 0, strictPort: false}});
 server.middlewares.use('/__invite_test', async (_req, res) => {res.setHeader('Content-Type', 'text/html');res.end(await server.transformIndexHtml('/__invite_test', '<html><body><div id="root"></div><script type="module" src="/'+entry+'"></script></body></html>'));});
 await server.listen();
 browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || '/snap/bin/chromium', args: ['--no-sandbox']});
 const page = await browser.newPage({viewport: {width: 1440, height: 900}});
 await page.goto('http://127.0.0.1:'+server.httpServer.address().port+'/__invite_test');
 const invite = page.getByRole('button', {name: 'Invite someone', exact: true});
 await invite.click();
 const all = page.getByRole('checkbox', {name: 'All libraries', exact: true});
 await all.uncheck();
 assert.equal(await page.getByRole('button', {name: 'Create invitation', exact: true}).isDisabled(), true);
 assert.equal(await page.evaluate(() => probe.calls.length), 0);
 await all.check();
 await page.getByRole('button', {name: 'Create invitation', exact: true}).evaluate(node => {
   const key = Object.keys(node).find(key => key.startsWith('__reactProps$'));
   window.staleCreate = node[key].onClick;
 });
 await page.keyboard.press('Escape');
 await page.waitForTimeout(200);
 await page.evaluate(() => window.staleCreate({}));
 assert.equal(await page.evaluate(() => probe.calls.length), 0, 'closed form handler performs zero I/O');
 await invite.click();
 await page.getByRole('button', {name: 'Create invitation', exact: true}).click();
 await page.waitForFunction(() => probe.calls.length === 1);
 await page.keyboard.press('Escape');
 await page.mouse.click(1, 1);
 assert.equal(await page.getByRole('dialog', {name: /Invite someone$/}).count(), 1);
 assert.equal(await page.getByRole('button', {name: 'Close', exact: true}).count(), 0, 'busy create does not expose a dismiss button');
 assert.equal(await page.getByRole('dialog', {name: /Invite someone$/}).count(), 1);
 await page.evaluate(() => probe.reject(503));
 await page.getByRole('button', {name: /Retry/}).waitFor();
 assert.equal(await page.evaluate(() => probe.reports), 0);
 assert.equal(await all.isChecked(), true);
 await page.getByRole('button', {name: /Retry/}).click();
 await page.waitForFunction(() => probe.calls.length === 2);
 assert.deepEqual(await page.evaluate(() => probe.calls[0]), await page.evaluate(() => probe.calls[1]));
 // An unreadable receipt is uncertain and must not discard the operation.
 await page.evaluate(() => probe.resolve('unknown'));
 await page.getByRole('button', {name: /Retry/}).waitFor();
 await page.getByRole('button', {name: /Retry/}).click();
 await page.waitForFunction(() => probe.calls.length === 3);
 assert.deepEqual(await page.evaluate(() => probe.calls[0]), await page.evaluate(() => probe.calls[2]));
 // A valid receipt clears the operation; another explicit invite gets a new id.
 await page.evaluate(() => probe.resolve('pending'));
 await page.waitForFunction(() => probe.issues === 1);
 const ready = page.getByRole('dialog', {name: 'Invitation ready', exact: true});
 await ready.getByRole('img', {name: /QR/i}).waitFor();
 assert.equal(await ready.getByText('Private invitation note').count(), 0, 'share card must not show private notes');
 assert.equal(await ready.getByRole('button', {name: 'Copy link', exact: true}).count(), 1);
 await page.getByRole('button', {name: 'Close', exact: true}).first().click();
 await invite.click();
 await page.getByRole('button', {name: 'Create invitation', exact: true}).click();
 await page.waitForFunction(() => probe.calls.length === 4);
 assert.notEqual(await page.evaluate(() => probe.calls[3].operationId), await page.evaluate(() => probe.calls[0].operationId));
 // A retired workspace cannot publish a late success or start a code issue.
 await page.evaluate(() => probe.unmount());
 await page.evaluate(() => probe.resolve('pending'));
 await page.waitForTimeout(200);
 assert.equal(await page.getByRole('dialog').count(), 0);
 assert.equal(await page.evaluate(() => probe.reports), 0);
 assert.equal(await page.evaluate(() => probe.issues), 1);
 await page.evaluate(() => probe.mount());
 await invite.click();
 await page.getByRole('button', {name: 'Create invitation', exact: true}).click();
 await page.waitForFunction(() => probe.calls.length === 5);
 await page.evaluate(() => probe.reject(401));
 await page.waitForFunction(() => probe.retirements === 1);
 assert.equal(await page.getByRole('dialog').count(), 0);
 console.log('PASS: empty subset, busy Escape/X, deferred retry, invalid receipt, unmount fence');
} finally {
 await browser?.close(); await server?.close(); await rm(entry, {force: true});
}
