// The real web app against the rebuilt server (server-next): the sign-in, profile and people screens
// beyond signing in itself. The server starts on a fresh data folder seeded with a small library, so
// a profile has Tags to block. Run it as server-next-journey.browser.mjs is run, plus the seed tool:
//
//   cd server-next && go build -o /path/portico-server ./cmd/portico-server && go build -o /path/portico-seed ./cmd/portico-seed
//   cd web && PORTICO_SERVER_NEXT_BIN=/path/portico-server PORTICO_SEED_BIN=/path/portico-seed node test/server-next-people-journey.browser.mjs
//
// Each step prints "pass" or "FAIL" and the script carries on where it can. It exits 1 when any step failed.
import {failureWords, startJourney, totp, until} from './server-next-harness.mjs';

const j = await startJourney({seedItems: 300});
const {call, signInAs, step} = j;
const password = ['journey', 'owner', 'Passphrase', '7'].join('-'), memberPassword = ['journey', 'member', 'Passphrase', '3'].join('-');
const kidPin = '4821';
await j.setUpOwner(password);
const owner = await signInAs('owner', password);
if ((await call('POST', '/v1/profiles', {name: 'Kid', color: 'coral', pin: kidPin}, owner.token)).status !== 200) throw new Error('The script could not add a profile.');
if ((await call('POST', '/v1/admin/members', {username: 'sam', password: memberPassword, profileName: 'Sam'}, owner.token)).status !== 200) throw new Error('The script could not add a member.');
const sam = await signInAs('sam', memberPassword);
if ((await call('POST', '/v1/profiles', {name: 'Junior', color: 'mint'}, sam.token)).status !== 200) throw new Error('The script could not add the member\'s profile.');
const profileId = async (token, name) => (await call('GET', '/v1/profiles', undefined, token)).body.items.find(p => p.name === name).id;
const kidId = await profileId(owner.token, 'Kid');

/** Picks a choice in one of the app's own drop-down lists. */
const choose = async (page, name, option) => {
  await page.getByRole('combobox', {name}).click();
  await page.getByRole('option', {name: option, exact: true}).click();
};

let secret = '';
/** A code the authenticator would show now, never the one just used (each code works once). */
let lastCode = '';
const freshCode = async () => {
  let code = totp(secret);
  while (code === lastCode) {await new Promise(resolve => setTimeout(resolve, 1000)); code = totp(secret);}
  return lastCode = code;
};

try {
  const context = await j.browser.newContext({viewport: {width: 1440, height: 900}});
  const page = await context.newPage(), requests = j.watch(page);
  page.on('response', async answer => {
    if (answer.request().method() === 'POST' && new URL(answer.url()).pathname === '/v1/me/two-factor' && answer.ok()) secret = (await answer.json()).secret;
  });
  const security = () => page.locator('#profile\\.twoStep').or(page.getByText('Two-step verification', {exact: true})).first();

  await step('the owner\'s Profile & account opens on the rebuilt server', async () => {
    await j.signInPage(page, 'owner', password, 'owner');
    await j.openSettings(page);
    await security().waitFor();
    await until('Two-step state', async () => requests.has('GET /v1/me/two-factor 200'));
    if (failureWords.test(await page.locator('main').innerText())) throw new Error('The page shows a failure.');
  });
  // The development server compiles each page the first time it is opened; after that, a step waits less.
  context.setDefaultTimeout(12000);

  await step('two-step sign-in turns on with an authenticator code and shows the recovery codes once', async () => {
    await page.getByRole('switch', {name: 'Two-step verification'}).click();
    await page.getByLabel('Current password').fill(password);
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByText('Enter this key in your authenticator app', {exact: false}).waitFor();
    await until('The key', async () => secret !== '');
    await page.getByLabel('Code', {exact: true}).fill(await freshCode());
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByText('Each code works once', {exact: false}).waitFor();
    if (!requests.has('POST /v1/me/two-factor/confirm 200')) throw new Error('Two-step was not confirmed.');
    await page.getByRole('button', {name: 'Done'}).click();
    const state = await call('GET', '/v1/me/two-factor', undefined, owner.token);
    if (!state.body.enabled || state.body.recoveryCodesLeft !== 10) throw new Error('The server does not have two-step on with ten codes: ' + JSON.stringify(state.body));
  });

  await step('new recovery codes need the password and a current code', async () => {
    await page.getByRole('button', {name: 'Make new codes…'}).click();
    await page.getByLabel('Current password').fill(password);
    await page.getByLabel('Authenticator code').fill(await freshCode());
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByRole('dialog').getByText('Each code works once', {exact: false}).waitFor();
    if (!requests.has('POST /v1/me/two-factor/recovery-codes 200')) throw new Error('No new codes were made.');
    await page.getByRole('button', {name: 'Done'}).click();
  });

  await step('a child profile\'s highest rating and blocked Tags are saved, and another device sees them', async () => {
    await page.getByRole('list', {name: /profiles/i}).getByRole('listitem').filter({hasText: 'Kid'}).getByRole('button', {name: 'Manage'}).click();
    await choose(page, 'Highest rating', 'PG-13');
    await until('The rating being saved', async () => (await call('GET', `/v1/profiles/${kidId}/restrictions`, undefined, owner.token)).body.maxRatingAge === 13);
    await page.getByRole('textbox', {name: 'Add a Tag'}).fill('Kids');
    await page.getByRole('button', {name: 'Add a Tag'}).click();
    const kidsTag = (await call('GET', `/v1/facets?facet=label&library=${(await call('GET', '/v1/libraries', undefined, owner.token)).body.items[0].id}`, undefined, owner.token)).body.values.find(v => v.label === 'Kids').value;
    await until('The Tag being blocked', async () => (await call('GET', `/v1/profiles/${kidId}/restrictions`, undefined, owner.token)).body.blockedLabels.includes(kidsTag));
    // Seen again from a second sign-in. (A reload on this server stays on "Still connecting" until Home moves to the
    // new routes, which is the browse change, not this area.)
    const again = await context.browser().newContext({viewport: {width: 1440, height: 900}});
    const second = await again.newPage();
    await j.signInPage(second, 'owner', password, 'owner', undefined, freshCode);
    await j.openSettings(second);
    await second.getByRole('list', {name: /profiles/i}).getByRole('listitem').filter({hasText: 'Kid'}).getByRole('button', {name: 'Manage'}).click();
    await second.getByRole('list', {name: 'Blocked Tags'}).getByText('Kids', {exact: true}).waitFor();
    await second.getByRole('combobox', {name: 'Highest rating'}).getByText('PG-13').waitFor();
    await again.close();
  });

  await step('Forgot PIN on the child\'s profile asks for the account password and two-step code, and the new PIN opens it', async () => {
    const child = await context.browser().newContext({viewport: {width: 1440, height: 900}});
    const kidPage = await child.newPage(), kidRequests = j.watch(kidPage);
    await j.signInPage(kidPage, 'owner', password, 'Kid', kidPin, freshCode);
    await j.openSettings(kidPage);
    await kidPage.getByRole('button', {name: 'Forgot PIN…'}).click();
    await kidPage.getByLabel('Password for owner').fill(password);
    await kidPage.getByRole('button', {name: 'Continue'}).click();
    await kidPage.getByLabel('Choose a four-digit PIN').fill('2468');
    await kidPage.getByRole('button', {name: 'Set new PIN'}).click();
    await kidPage.getByLabel('Authenticator or recovery code').fill(await freshCode());
    await kidPage.getByText('PIN changed').waitFor();
    if (!kidRequests.has('PUT /v1/profiles/' + kidId + '/pin 200')) throw new Error('The PIN was not set by this device.');
    const challenge = await call('POST', '/v1/auth/sign-in', {username: 'owner', password, device: {installationId: 'journey-script-installation-pin-check', name: 'Check'}});
    const signedIn = await call('POST', '/v1/auth/two-factor', {challenge: challenge.body.challenge.token, code: await freshCode()});
    const old = await call('POST', `/v1/profiles/${kidId}/select`, {pin: kidPin}, signedIn.body.session.accessToken);
    await new Promise(resolve => setTimeout(resolve, 2500)); // a wrong PIN makes this device wait two seconds
    const fresh = await call('POST', `/v1/profiles/${kidId}/select`, {pin: '2468'}, signedIn.body.session.accessToken);
    if (old.status === 200 || fresh.status !== 200) throw new Error(`The old PIN answered ${old.status} and the new one ${fresh.status}.`);
    if (await kidPage.getByLabel('Username').count()) throw new Error('The child\'s device was signed out.');
    await child.close();
  });

  await step('two-step turns off with the password and a code, and this device stays signed in', async () => {
    await page.getByRole('switch', {name: 'Two-step verification'}).click();
    await page.getByLabel('Current password').fill(password);
    await page.getByLabel('Authenticator code').fill(await freshCode());
    await page.getByRole('button', {name: 'Continue'}).click();
    await until('Two-step off', async () => !(await call('GET', '/v1/me/two-factor', undefined, owner.token)).body.enabled);
    await until('The switch showing off', async () => !await page.getByRole('switch', {name: 'Two-step verification'}).isChecked());
    if (await page.getByLabel('Username').count()) throw new Error('This device was signed out.');
  }, page);

  await step('a member limits a profile on their own account', async () => {
    const other = await context.browser().newContext({viewport: {width: 1440, height: 900}});
    const memberPage = await other.newPage();
    await j.signInPage(memberPage, 'sam', memberPassword, 'Sam');
    await j.openSettings(memberPage);
    await memberPage.getByRole('list', {name: /profiles/i}).getByRole('listitem').filter({hasText: 'Junior'}).getByRole('button', {name: 'Manage'}).click();
    await choose(memberPage, 'Highest rating', 'G');
    const juniorId = await profileId(sam.token, 'Junior');
    await until('The rating being saved', async () => (await call('GET', `/v1/profiles/${juniorId}/restrictions`, undefined, sam.token)).body.maxRatingAge === 0);
    await other.close();
  });
} finally {
  await j.finish();
}
