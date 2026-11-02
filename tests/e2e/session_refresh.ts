// Session renewal end to end: one client renewing repeatedly, and two clients on the SAME account
// (different installations, as two devices or two browser origins are) signing in and renewing in
// turn. Every renewal must succeed, and neither client's sign-in may end the other's.
//
//   python3 tests/e2e/server.py --binary <devtrust-server> --media-cache <dir> -- \
//     node --experimental-strip-types tests/e2e/session_refresh.ts
import {readFileSync} from 'node:fs';
import {randomBytes, randomUUID} from 'node:crypto';

const base = process.env.PORTICO_E2E_SERVER_URL!, credentialsFile = process.env.PORTICO_E2E_CREDENTIALS_FILE!;
if (!base || !credentialsFile) throw new Error('Run under tests/e2e/server.py.');
const member = JSON.parse(readFileSync(credentialsFile, 'utf8')).member as {username: string; password: string};
const ROUNDS = Number(process.env.REFRESH_ROUNDS ?? 6);

type Session = {accessToken: string; refreshToken?: string; tokenGeneration: string; expiresAt: string; viewer: {role: string; accountId: string}};

class Client {
  readonly installation = randomBytes(32).toString('base64url');
  session?: Session;
  readonly name: string; readonly origin?: string;
  constructor(name: string, origin?: string) {this.name = name; this.origin = origin;}
  async call(path: string, method = 'GET', body?: unknown, token = this.session?.accessToken): Promise<{status: number; body: any}> {
    // Many sign-ins and renewals from one address meet the server's auth rate limit; wait as a
    // real client does (Retry-After), which never changes what the server decides.
    for (let attempt = 0; ; attempt++) {
      const answer = await this.once(path, method, body, token);
      if (answer.status !== 429 || attempt >= 6) return answer;
      await new Promise(resolve => setTimeout(resolve, Math.min(30, Number(answer.retryAfter) || 2) * 1000));
    }
  }
  async once(path: string, method: string, body: unknown, token: string | undefined): Promise<{status: number; body: any; retryAfter: string | null}> {
    const response = await fetch(base + path, {method, redirect: 'error', headers: {
      Accept: 'application/json', ...(body === undefined ? {} : {'Content-Type': 'application/json'}),
      ...(token ? {Authorization: `Bearer ${token}`} : {}), ...(this.origin ? {Origin: this.origin} : {}),
      'X-Portico-Installation-Id': this.installation, 'X-Portico-Device-Name': `Session e2e ${this.name}`, 'X-Portico-Device-Platform': 'script',
    }, ...(body === undefined ? {} : {body: JSON.stringify(body)})});
    const text = await response.text();
    let parsed: any; try { parsed = text ? JSON.parse(text) : undefined; } catch { parsed = text; }
    return {status: response.status, body: parsed, retryAfter: response.headers.get('Retry-After')};
  }
  async signIn() {
    const signed = await this.call('/v1/direct/sign-in', 'POST', {username: member.username, password: member.password}, '');
    if (signed.status !== 200) throw new Error(`${this.name} sign-in ${signed.status} ${JSON.stringify(signed.body).slice(0, 200)}`);
    this.session = signed.body.session;
    if (this.session!.viewer.role === 'account') {
      const profile = signed.body.profiles.find((p: any) => !p.pinRequired) ?? signed.body.profiles[0];
      const picked = await this.call(`/v1/direct/profiles/${encodeURIComponent(profile.id)}/select`, 'POST', {installationId: this.installation});
      if (picked.status !== 200) throw new Error(`${this.name} profile ${picked.status} ${JSON.stringify(picked.body).slice(0, 200)}`);
      this.session = picked.body.session;
    }
    if (!this.session!.refreshToken) throw new Error(`${this.name} got no refresh credential`);
  }
  async refresh() {
    const renewed = await this.call('/v1/auth/refresh', 'POST', {refreshToken: this.session!.refreshToken, installationId: this.installation, requestId: randomUUID().replace(/-/g, '')}, '');
    if (renewed.status !== 200) throw new Error(`${this.name} refresh ${renewed.status} ${JSON.stringify(renewed.body).slice(0, 300)}`);
    if (BigInt(renewed.body.tokenGeneration) <= BigInt(this.session!.tokenGeneration)) throw new Error(`${this.name} refresh did not advance`);
    this.session = renewed.body;
  }
  async me() {
    const me = await this.call('/v1/me');
    if (me.status !== 200) throw new Error(`${this.name} /v1/me ${me.status} ${JSON.stringify(me.body).slice(0, 200)}`);
  }
}

const results: string[] = [];
async function scenario(name: string, run: () => Promise<void>) {
  try { await run(); results.push(`PASS ${name}`); } catch (error) { results.push(`FAIL ${name}: ${error instanceof Error ? error.message : error}`); }
}

await scenario(`one client renews ${ROUNDS} times and stays signed in`, async () => {
  const a = new Client('solo'); await a.signIn();
  for (let i = 0; i < ROUNDS; i++) { await a.refresh(); await a.me(); }
});

await scenario('a second client signing in to the same account leaves the first signed in', async () => {
  const a = new Client('first'), b = new Client('second');
  await a.signIn(); await a.me();
  await b.signIn(); await b.me();
  await a.me(); await a.refresh(); await a.me();
});

await scenario(`two clients on the same account renew in turn ${ROUNDS} times`, async () => {
  const a = new Client('left'), b = new Client('right');
  await a.signIn(); await b.signIn();
  for (let i = 0; i < ROUNDS; i++) { await a.refresh(); await b.refresh(); await a.me(); await b.me(); }
});

await scenario('two browsers on the allowed origin (each its own installation) coexist and renew', async () => {
  const a = new Client('browser-a', base), b = new Client('browser-b', base);
  await a.signIn(); await b.signIn();
  for (let i = 0; i < ROUNDS; i++) { await a.refresh(); await b.refresh(); await a.me(); await b.me(); }
});

await scenario('a renewal claiming another installation is refused and ends nothing', async () => {
  const a = new Client('bound'); await a.signIn();
  const stolen = await a.call('/v1/auth/refresh', 'POST', {refreshToken: a.session!.refreshToken, installationId: randomBytes(32).toString('base64url'), requestId: randomUUID().replace(/-/g, '')}, '');
  if (stolen.status !== 401) throw new Error(`a mismatched installation got ${stolen.status}`);
  await a.me(); await a.refresh(); await a.me();
});

await scenario('a client signing in again replaces only its own session', async () => {
  const a = new Client('stays'), b = new Client('again');
  await a.signIn(); await b.signIn(); await b.signIn();
  await a.me(); await a.refresh(); await a.me(); await b.me();
});

for (const line of results) console.log(line);
process.exit(results.some(line => line.startsWith('FAIL')) ? 1 : 0);
