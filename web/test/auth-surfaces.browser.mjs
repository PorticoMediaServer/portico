import assert from 'node:assert/strict';

// The caller owns the existing Vite server and one isolated public-specimen page.
// Real controls/keyboard events only; no auth API, CSS state or production mocks.
export async function checkAccountSurfaces(page, origin) {
  const requests = [], errors = [];
  page.on('request', request => {if (new URL(request.url()).pathname.startsWith('/v1/')) requests.push(request.url());});
  page.on('pageerror', error => errors.push(error.message));
  const open = async frame => {
    await page.goto(`${origin}/src/dev/entries/accounts.html?frame=${frame}`, {waitUntil: 'networkidle'});
    await page.evaluate(() => document.fonts.ready);
  };
  const action = () => page.evaluate(() => JSON.parse(document.documentElement.dataset.accountAction));
  await page.setViewportSize({width: 1440, height: 900});
  await open('13');
  assert.equal(await page.getByRole('heading', {name: 'Who’s watching on Portico demo?', exact: true}).count(), 1);
  assert.equal(await page.getByRole('button', {name: 'Sam Restricted', exact: true}).isDisabled(), true);
  assert.equal(await page.getByRole('button', {name: /Manage profiles|Add profile|Not shared/}).count(), 0);
  assert.equal(await page.getByRole('button', {name: 'Alex Protected by a PIN', exact: true}).evaluate(e => e === document.activeElement), true);
  await page.keyboard.press('Tab');
  assert.equal(await page.getByRole('button', {name: 'Kids', exact: true}).evaluate(e => e === document.activeElement), true);
  await page.keyboard.press('Enter'); assert.deepEqual(await action(), {kind: 'profile', value: 'kids'});

  await open('14');
  const pin = page.getByRole('textbox', {name: 'Four-digit PIN', exact: true});
  assert.equal(await pin.getAttribute('autocomplete'), 'one-time-code');
  assert.equal(await pin.getAttribute('inputmode'), 'numeric');
  assert.equal(await pin.getAttribute('aria-invalid'), 'true');
  assert.equal(await pin.evaluate(e => e === document.activeElement), true);
  await pin.fill('1x٢'); assert.equal(await pin.inputValue(), '1');
  await pin.fill('1234'); assert.deepEqual(await action(), {kind: 'pin', value: '1234'});
  assert.equal(await pin.inputValue(), '');
  const remember = page.getByRole('checkbox', {name: 'Remember this profile in this browser tab', exact: true});
  await remember.check(); assert.equal(await remember.isChecked(), true);
  await page.getByRole('button', {name: 'Other profiles', exact: true}).click(); assert.deepEqual(await action(), {kind: 'profiles'});

  await open('4');
  const username = page.getByRole('textbox', {name: 'Username', exact: true});
  const password = page.getByLabel('Password', {exact: true});
  assert.equal(await username.getAttribute('autocomplete'), 'username');
  assert.equal(await password.getAttribute('autocomplete'), 'current-password');
  assert.equal(await page.getByRole('button', {name: 'Sign in', exact: true}).isDisabled(), true);
  await password.fill('specimen-only-secret');
  await page.getByRole('button', {name: 'Show password', exact: true}).click();
  assert.equal(await password.getAttribute('type'), 'text');
  await page.getByRole('button', {name: 'Hide password', exact: true}).click();
  assert.equal(await password.getAttribute('type'), 'password');
  await page.getByRole('button', {name: 'T Theo', exact: true}).click();
  assert.equal(await username.inputValue(), 'theo'); assert.equal(await password.inputValue(), '');
  await password.fill('specimen-only-secret'); await password.press('Enter');
  assert.deepEqual(await action(), {kind: 'sign-in', value: {username: 'theo', password: 'specimen-only-secret'}});

  await open('4b');
  assert.equal(await page.locator('input').count(), 0, 'a Portico server never offers a password field');
  await page.getByRole('button', {name: 'Continue to Portico Account', exact: true}).click();
  assert.deepEqual(await action(), {kind: 'portico'});
  const pending = await page.evaluate(async () => {
    const {secureBrowserPending} = await import('/src/platform/secure-pending.ts');
    const open = indexedDB.open.bind(indexedDB); let opened = 0;
    indexedDB.open = (...args) => {opened++; return open(...args);};
    const codes = [];
    try {
      const store = secureBrowserPending('portico.secure-pending-browser-specimen');
      for (const run of [() => store.read(), () => store.change(() => ({specimen: true}))]) {
        try {await run();} catch (error) {codes.push(error.code);}
      }
      return {protocol: location.protocol, secureContext: isSecureContext, opened, codes};
    } finally {indexedDB.open = open;}
  });
  assert.equal(pending.protocol, 'http:');
  assert.equal(pending.opened, 0, 'the actual HTTP browser must refuse before IndexedDB access');
  assert.deepEqual(pending.codes, ['secure_context_required', 'secure_context_required']);
  assert.deepEqual(errors, []); assert.deepEqual(requests, [], 'a presentation specimen must not contact any auth service');
  return {profiles: true, keyboard: true, pin: true, remembered: true, passwordManager: true, porticoKind: true, securePendingHTTP: pending, errors, apiRequests: requests.length};
}
