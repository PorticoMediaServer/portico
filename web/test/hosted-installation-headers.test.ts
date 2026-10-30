import test from 'node:test';
import assert from 'node:assert/strict';
import {setInstallationIdentity} from '@core/installation.ts';
import {BrowserAccountApi, hostedInstallationHeaders, issuesHostedCredentials} from '../src/platform/account.ts';

const identity = {installationId: 'i'.repeat(40), name: 'Chrome on Linux', platform: 'web', app: 'Portico Web', appVersion: '1.2.3'};
const expected = {
  'X-Portico-Device-Name': 'Chrome on Linux', 'X-Portico-Platform': 'web', 'X-Portico-App': 'Portico Web',
  'X-Portico-Version': '1.2.3', 'X-Portico-Installation-Id': 'i'.repeat(40),
};

test('Hosted device headers map once and reach only the requests that create a session family', async () => {
  const calls: {url: string; headers: Record<string, string>}[] = [];
  const originalFetch = globalThis.fetch;
  const originalDocument = (globalThis as unknown as {document?: unknown}).document;
  const originalStorage = (globalThis as unknown as {localStorage?: unknown}).localStorage;
  (globalThis as unknown as {document: unknown}).document = {cookie: ''};
  (globalThis as unknown as {localStorage: unknown}).localStorage = {getItem: () => null, setItem: () => {}};
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({url: String(url), headers: init?.headers as Record<string, string>});
    return {ok: true, status: 200, json: async () => ({}), headers: {get: () => null}};
  }) as typeof fetch;
  try {
    assert.deepEqual(hostedInstallationHeaders({
      'X-Portico-Installation-Id': 'i'.repeat(40), 'X-Portico-Device-Name': 'Chrome on Linux',
      'X-Portico-Device-Platform': 'web', 'X-Portico-App': 'Portico Web', 'X-Portico-App-Version': '1.2.3',
    }), expected);
    setInstallationIdentity(identity);
    const api = new BrowserAccountApi('https://accounts.example',{callerOrigin:'https://media.example'});
    for (const request of [
      ['/v1/sessions', 'POST', {}],
      ['/v1/registrations/reg_1/verify', 'POST', {code: '123456'}],
      ['/v1/identity/mfa/complete', 'POST', {}],
      ['/v1/identity/transactions/tx_1/complete', 'POST', {}],
    ] as const) {
      calls.length = 0;
      await api.send(request[0], request[1], request[2]);
      const headers = calls[0]!.headers;
      for (const [name, value] of Object.entries(expected)) assert.equal(headers[name], value, `${request[0]} ${name}`);
      assert.equal(headers['X-Portico-Device-Platform'], undefined, 'the shared name is mapped to Hosted’s accepted name');
      assert.equal(headers['X-Portico-App-Version'], undefined);
    }
    calls.length = 0;
    await api.send('/v1/account', 'GET');
    assert.deepEqual(Object.keys(calls[0]!.headers).filter(name => name.startsWith('X-Portico-')), [], 'ordinary management reads carry no installation claim');
    calls.length = 0;
    await api.send('/v1/sessions/refresh', 'POST', {});
    assert.deepEqual(Object.keys(calls[0]!.headers).filter(name => name.startsWith('X-Portico-')), [], 'refresh creates no device row and carries no claim');
    assert.equal(issuesHostedCredentials('/v1/sessions', 'POST'), true);
    assert.equal(issuesHostedCredentials('/v1/sessions', 'GET'), false);
    assert.equal(issuesHostedCredentials('/v1/sessions/refresh', 'POST'), false);
    assert.equal(issuesHostedCredentials('/v1/accounts', 'POST'), false);
    setInstallationIdentity(undefined);
    calls.length = 0;
    await api.send('/v1/sessions', 'POST', {});
    assert.deepEqual(Object.keys(calls[0]!.headers).filter(name => name.startsWith('X-Portico-')), [], 'an unavailable claim is never synthesized');
  } finally {
    globalThis.fetch = originalFetch;
    (globalThis as unknown as {document?: unknown}).document = originalDocument;
    (globalThis as unknown as {localStorage?: unknown}).localStorage = originalStorage;
    setInstallationIdentity(undefined);
  }
});
