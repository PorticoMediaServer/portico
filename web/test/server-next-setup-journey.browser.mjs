// The real web app against the rebuilt server (server-next): first-run setup on a fresh server and the first libraries.
// Setup names the server and lists the local owner; the first library is added by choosing its folder in the folder explorer,
// another from Home by typing its path; Home then shows no message about something that is not on this server yet.
//
//   cd server-next && go build -o /path/portico-server ./cmd/portico-server
//   cd web && PORTICO_SERVER_NEXT_BIN=/path/portico-server node test/server-next-setup-journey.browser.mjs
//
// Each step prints "pass" or "FAIL" and the script carries on where it can. It exits 1 when any step failed.
import {mkdir, mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {failureWords, startJourney, until} from './server-next-harness.mjs';

const j = await startJourney();
const {call, step} = j;
const password = ['journey', 'owner', 'Passphrase', '7'].join('-');
// A folder of the server's machine to make a library from, outside the server's own data folder (which the server refuses).
const media = await mkdtemp(join(tmpdir(), 'portico-next-media-')), music = join(media, 'Music');
await mkdir(join(music, 'Kelquin', 'First Light'), {recursive: true});
await writeFile(join(music, 'Kelquin', 'First Light', '01 - Dawn.flac'), '');

try {
  const context = await j.browser.newContext({viewport: {width: 1440, height: 900}});
  const page = await context.newPage(), requests = j.watch(page);

  await step('setup keeps the server name typed, and lists the local owner once the owner is made', async () => {
    await page.goto(j.origin + '/', {waitUntil: 'domcontentloaded'});
    await page.getByLabel('Server name').fill('Living room');
    await page.getByRole('radio', {name: /Local sign-in/}).check();
    await page.getByLabel('Username').fill('owner');
    await page.getByLabel('Password', {exact: true}).fill(password);
    await page.getByLabel('Confirm password').fill(password);
    await page.getByRole('button', {name: 'Create owner'}).click();
    await page.getByLabel('Setup code').fill((await readFile(join(j.data, 'setup-code.txt'), 'utf8')).trim());
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByRole('heading', {name: 'Add your first library'}).waitFor();
    const named = (await call('GET', '/v1/system')).body.name;
    if (named !== 'Living room') throw new Error(`The server is called "${named}".`);
    const steps = await page.getByLabel('Setup steps').innerText();
    if (/Portico Account/.test(steps) || !/Owner/.test(steps) || !/owner/.test(steps)) throw new Error('The setup steps read: ' + steps.replace(/\s+/g, ' '));
    // A reload at the library step still knows the server uses local sign-in.
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByRole('heading', {name: 'Add your first library'}).waitFor();
    const again = await page.getByLabel('Setup steps').innerText();
    if (/Portico Account/.test(again)) throw new Error('After a reload the setup steps read: ' + again.replace(/\s+/g, ' '));
  });

  await step('the first library is added by choosing its folder in the folder explorer, and scanned', async () => {
    await page.getByRole('button', {name: /^Music/}).click();
    await page.getByRole('button', {name: 'Choose folders…'}).click();
    const explorer = page.getByRole('dialog');
    await until('The places to start from', async () => requests.has('GET /v1/admin/filesystem 200'));
    await explorer.getByLabel('Folder path').fill(media);
    await explorer.getByRole('button', {name: 'Go'}).click();
    await explorer.getByRole('button', {name: /^Music/}).first().click();
    await explorer.getByRole('button', {name: /^Kelquin/}).waitFor();
    await explorer.getByRole('button', {name: 'Choose this folder'}).click();
    await explorer.getByRole('button', {name: /^Use /}).click();
    await page.getByText(music, {exact: true}).first().waitFor();
    await page.getByRole('button', {name: 'Add library and start scanning'}).click();
    const owner = await j.signInAs('owner', password);
    await until('The library, scanned', async () => (await call('GET', '/v1/admin/libraries', undefined, owner.token)).body.items
      .some(library => library.kind === 'music' && library.lastScanFinishedAt), 300);
  });

  await step('from Home, a library is added by typing its folder\'s path, with a trailing slash, in the folder explorer', async () => {
    // Home offers Add library while the server has none, so the first one is removed first, as another device would.
    const owner = await j.signInAs('owner', password);
    for (const library of (await call("GET", "/v1/admin/libraries", undefined, owner.token)).body.items) {
      const removed = await call("DELETE", `/v1/admin/libraries/${library.id}?confirm=${encodeURIComponent(library.name)}`, undefined, owner.token);
      if (removed.status !== 202) throw new Error("The script could not remove a library: " + removed.status);
    }
    await until("The library gone", async () => !(await call("GET", "/v1/admin/libraries", undefined, owner.token)).body.items.length, 300);
    await page.goto(j.origin + '/', {waitUntil: 'domcontentloaded'});
    const shell = page.getByRole('button', {name: /account menu/}), username = page.getByLabel('Username');
    await shell.or(username).first().waitFor();
    if (await username.isVisible()) await j.signInPage(page, 'owner', password);
    await page.getByRole('button', {name: 'Add library'}).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('button', {name: 'Choose folders…'}).click();
    const explorer = page.getByRole('dialog').last();
    await explorer.getByLabel('Folder path').fill(music + '/');
    await explorer.getByRole('button', {name: 'Go'}).click();
    await explorer.getByRole('button', {name: /^Kelquin/}).waitFor();
    await explorer.getByRole('button', {name: 'Choose this folder'}).click();
    await explorer.getByRole('button', {name: /^Use /}).click();
    await dialog.getByRole('button', {name: 'Add library'}).click();
    await until('The library', async () => (await call('GET', '/v1/admin/libraries', undefined, owner.token)).body.items.some(library => library.folders.some(folder => folder.path === music)), 300);
    await dialog.waitFor({state: 'hidden'});
  }, page);

  await step('Home shows no message about something this server does not have yet', async () => {
    await page.goto(j.origin + '/', {waitUntil: 'domcontentloaded'});
    const shell = page.getByRole('button', {name: /account menu/});
    const username = page.getByLabel('Username');
    await shell.or(username).first().waitFor();
    if (await username.isVisible()) await j.signInPage(page, 'owner', password);
    await page.waitForTimeout(5000);
    const text = await page.locator('body').innerText();
    if (/available anymore/.test(text) || failureWords.test(text)) throw new Error('Home shows: ' + text.slice(0, 200).replace(/\s+/g, ' '));
  }, page);
} finally {
  await rm(media, {recursive: true, force: true});
  await j.finish();
}
