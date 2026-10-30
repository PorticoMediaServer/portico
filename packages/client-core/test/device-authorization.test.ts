import test, { type TestContext } from "node:test";
import assert from "node:assert/strict";
import { DeviceAuthorizationService, ApiError } from "../src/index.ts";
// Each test owns its clock. Waiting 10-25 real milliseconds races the second
// scheduled poll against the assertion when other test processes saturate CPU.
async function settle() { for (let n = 0; n < 32; n++) await Promise.resolve(); }
function clock(t: TestContext) {
 t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: 1800000000000 });
 return async (ms = 1) => { t.mock.timers.tick(ms); await settle(); };
}
const authorization = {
  deviceCode: "private-secret",
  userCode: "ABCD-EFGH",
  verificationUri: "https://example.com/device",
  verificationUriComplete: "https://example.com/device#code=ABCD-EFGH",
  expiresIn: 600,
  expiresAt: new Date(1800000000000 + 600000).toISOString(),
  interval: 0,
};
const session = {
  accessToken: "private-token",
  expiresAt: "2099-01-01T00:00:00Z",
  familyId: 'family_A',
  refreshToken: 'refresh_A',
  refreshExpiresAt: '2099-01-02T00:00:00Z',
  account: { id: "a", username: "a", displayName: "A" },
};
const options = { minimumPollMs: 1, requestTimeoutMs: 100 };
test("device secret never enters snapshot and approval is delivered", async (t) => {
  const advance = clock(t);
  let count = 0;
  const service = new DeviceAuthorizationService(
    {
      create: async () => authorization,
      poll: async () => {
        if (++count === 1)
          throw new ApiError(400, "authorization_pending", "Pending");
        return session;
      },
      cancel: async () => {},
      revoke: async () => {},
    },
    options,
    async () => crypto.randomUUID()
  );
  await service.start("TV", "tvos");
  assert.equal(
    JSON.stringify(service.getSnapshot()).includes("private-secret"),
    false
  );
  await advance(); // pending response schedules the next poll
  assert.equal(count, 1);
  await advance();
  assert.equal(service.getSnapshot().phase, "approved");
  assert.equal(count, 2);
  service.cancel();
});
test("cancel fences late token and revokes returned session", async (t) => {
  const advance = clock(t);
  let resolve: any;
  let revoked = 0,
    cancelled = 0;
  const service = new DeviceAuthorizationService(
    {
      create: async () => authorization,
      poll: () => new Promise((r) => (resolve = r)),
      cancel: async () => {
        cancelled++;
      },
      revoke: async () => {
        revoked++;
      },
    },
    options,
    async () => crypto.randomUUID()
  );
  await service.start("TV", "tvos");
  await advance();
  service.cancel();
  resolve(session);
  await advance();
  assert.equal(service.getSnapshot().phase, "idle");
  assert.equal(revoked, 1);
  assert.equal(cancelled, 1);
});
test("late create after local timeout is cancelled", async (t) => {
  const advance = clock(t);
  let resolve: any;
  let cancelled = 0;
  const service = new DeviceAuthorizationService(
    {
      create: () => new Promise((r) => (resolve = r)),
      poll: async () => session,
      cancel: async () => {
        cancelled++;
      },
      revoke: async () => {},
    },
    { minimumPollMs: 1, requestTimeoutMs: 5 },
    async () => crypto.randomUUID()
  );
  const started = service.start("TV", "tvos");
  await settle();
  await advance(5);
  await advance(5);
  await started;
  assert.equal(service.getSnapshot().phase, "error");
  resolve(authorization);
  await advance();
  assert.equal(cancelled, 1);
  service.cancel();
});

test('ambiguous create retries the same private request ID', async (t) => {
  clock(t);
  const ids:string[]=[];
  const service=new DeviceAuthorizationService({create:async(requestId)=>{ids.push(requestId);if(ids.length===1)throw new Error('connection lost');return authorization},poll:async()=>session,cancel:async()=>{},revoke:async()=>{}},options,async()=>crypto.randomUUID());
  await service.start('TV','tvos');assert.equal(ids.length,2);assert.equal(ids[0],ids[1]);service.cancel();
});

test('approval remains cancellable until the host accepts ownership', async(t)=>{
 const advance = clock(t);
 let cancelled=0;
 const service=new DeviceAuthorizationService({create:async()=>authorization,poll:async()=>session,cancel:async()=>{cancelled++},revoke:async()=>{}},options,async()=>crypto.randomUUID());
 await service.start('TV','tvos');await advance();service.cancel();assert.equal(cancelled,1);assert.equal(service.accept(session),false);
 await service.start('TV','tvos');await advance();assert.equal(service.accept(session),true);service.cancel();assert.equal(cancelled,1);
});

test('slow_down backs off and denial is terminal', async(t)=>{
 const advance = clock(t);
 let polls=0;
 const service=new DeviceAuthorizationService({create:async()=>authorization,poll:async()=>{polls++;throw new ApiError(429,'slow_down','Wait',true,10)},cancel:async()=>{},revoke:async()=>{}},options,async()=>crypto.randomUUID());
 await service.start('TV','tvos');await advance();await advance(9999);assert.equal(polls,1);assert.equal(service.getSnapshot().phase,'waiting');service.cancel();
 const denied=new DeviceAuthorizationService({create:async()=>authorization,poll:async()=>{throw new ApiError(400,'access_denied','This request was denied.')},cancel:async()=>{},revoke:async()=>{}},options,async()=>crypto.randomUUID());
 await denied.start('TV','tvos');await advance();assert.equal(denied.getSnapshot().phase,'error');assert.equal(denied.getSnapshot().error,'This request was denied.');denied.cancel();
});

test('activation adapter shares the sibling Hosted outage gate', async (t) => {
  const {HttpDeviceAuthorizationApi, hostedGate} = await import('../src/index.ts');
  const api = new HttpDeviceAuthorizationApi('https://activation-gate.example');
  const gate = hostedGate('https://activation-gate.example/');
  const fetch = t.mock.method(globalThis, 'fetch', async () => { throw new Error('must not fetch'); });
  await assert.rejects(gate.run('interactive', async () => { throw new ApiError(503, 'unavailable', 'Down', true, 120); }));
  await assert.rejects(api.poll('secret'), (e: unknown) => e instanceof ApiError && e.code === 'account_service_recovering');
  assert.equal(fetch.mock.callCount(), 0);
  gate.succeeded();
});

test('activation keeps its code and sends no polls at 30, 60 or 90 seconds of a sibling outage', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  t.mock.method(Math, 'random', () => 0);
  const api = new HttpDeviceAuthorizationApi('https://activation-schedule.example');
  const fetch = t.mock.method(globalThis, 'fetch', async (_url: unknown, init?: RequestInit) => {
    return new Response(JSON.stringify(init?.body?.toString().includes('deviceName') ? {...authorization, interval: 5} : session), {status: 200});
  });
  const service = new DeviceAuthorizationService(api, {minimumPollMs: 5000, requestTimeoutMs: 1000}, async () => crypto.randomUUID());
  await service.start('TV', 'tvos');
  await assert.rejects(api.gate.run('interactive', async () => {throw new ApiError(503, 'unavailable', 'Down', true, 120);}));
  for (let n = 0; n < 3; n++) {
    await advance(30000);
    assert.equal(fetch.mock.callCount(), 1);
    assert.equal(service.getSnapshot().phase, 'waiting');
    assert.equal(service.getSnapshot().userCode, authorization.userCode);
  }
  await advance(30000);
  assert.equal(fetch.mock.callCount(), 2);
  assert.equal(service.getSnapshot().phase, 'approved');
  service.accept(service.getSnapshot().session!);
  service.cancel();
});

test('HTTP-date Retry-After survives a 30-second protocol slowdown cap', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  t.mock.method(Math, 'random', () => 0);
  const api = new HttpDeviceAuthorizationApi('https://activation-protocol.example');
  let polls = 0;
  const fetch = t.mock.method(globalThis, 'fetch', async (_url: unknown, init?: RequestInit) => {
    if (init?.body?.toString().includes('deviceName')) return new Response(JSON.stringify({...authorization, interval: 30}));
    polls++;
    if (polls === 1) return new Response(JSON.stringify({error: {code: 'slow_down', message: 'Wait.', retryable: true}}), {status: 429, headers: {'Retry-After': new Date(Date.now() + 120000).toUTCString()}});
    return new Response(JSON.stringify(session));
  });
  const service = new DeviceAuthorizationService(api, {minimumPollMs: 5000, requestTimeoutMs: 1000}, async () => crypto.randomUUID());
  await service.start('TV', 'tvos');
  await advance(30000);
  assert.equal(api.gate.blockedFor(), 0, 'slow_down is a protocol answer');
  for (let n = 0; n < 3; n++) {await advance(30000); assert.equal(polls, 1);}
  await advance(30000);
  assert.equal(polls, 2);
  assert.equal(fetch.mock.callCount(), 3);
  service.accept(service.getSnapshot().session!);
  service.cancel();
});

test('manual create and cancel remain interactive while automatic cleanup waits', async (t) => {
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  const api = new HttpDeviceAuthorizationApi('https://activation-actions.example');
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response(null, {status: 204}));
  api.gate.failed(120);
  await assert.rejects(api.cancel('secret', 'automatic'));
  await assert.rejects(api.revoke(session));
  await assert.rejects(api.create('request', 'TV', 'tvos', '1', undefined, 'automatic'));
  assert.equal(fetch.mock.callCount(), 0);
  await api.cancel('secret');
  api.gate.failed(120);
  await api.create('request', 'TV', 'tvos', '1');
  assert.equal(fetch.mock.callCount(), 2);
});

test('ambiguous HTTP create waits for the outage floor and reuses its request ID', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  t.mock.method(Math, 'random', () => 0);
  const api = new HttpDeviceAuthorizationApi('https://activation-create-retry.example');
  const ids: string[] = [];
  t.mock.method(globalThis, 'fetch', async (_url: unknown, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body));
    if (body.requestId) ids.push(body.requestId);
    if (ids.length === 1) return new Response('Unavailable', {status: 503, headers: {'Retry-After': new Date(Date.now() + 120000).toUTCString()}});
    return new Response(JSON.stringify(authorization));
  });
  const service = new DeviceAuthorizationService(api, {minimumPollMs: 5000, requestTimeoutMs: 1000}, async () => crypto.randomUUID());
  const started = service.start('TV', 'tvos');
  await settle();
  await advance(119999);
  assert.equal(ids.length, 1);
  await advance(1);
  await started;
  assert.equal(ids.length, 2);
  assert.equal(ids[0], ids[1]);
  assert.equal(service.getSnapshot().phase, 'waiting');
  service.cancel();
});

test('an expired activation clears locally without bypassing a sibling outage', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  const api = new HttpDeviceAuthorizationApi('https://activation-expiry.example');
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify({...authorization, expiresAt: new Date(Date.now() + 10000).toISOString()})));
  const service = new DeviceAuthorizationService(api, options, async () => crypto.randomUUID());
  await service.start('TV', 'tvos');
  api.gate.failed(120);
  await advance(10000);
  assert.equal(service.getSnapshot().phase, 'error');
  assert.equal(service.getSnapshot().userCode, undefined);
  assert.equal(fetch.mock.callCount(), 1, 'expiry cleanup is automatic');
});

test('web Hosted code review reports an outage and local approval never shares that gate', async (t) => {
  const {HttpLocalApi, hostedGate} = await import('../src/index.ts');
  const {SetupCodeApprover} = await import('../src/setup-code.ts');
  const origin = 'https://activation-review.example';
  const api = new HttpLocalApi(origin, 'token', async () => new Response('Unavailable', {status: 503, headers: {'Retry-After': '120'}}));
  const approver = new SetupCodeApprover(api, 'hosted');
  await assert.rejects(approver.review('ABCD-EFGH'));
  assert.ok(hostedGate(origin).blockedFor() >= 120000);
  const before = hostedGate(origin).getSnapshot();
  await assert.rejects(new SetupCodeApprover(api, 'local').review('ABCD-EFGH'));
  assert.deepEqual(hostedGate(origin).getSnapshot(), before);
  hostedGate(origin).succeeded();
});

test('durable activation keeps its code behind a sibling gate and an explicit start still creates', async (t) => {
  const advance = clock(t);
  const {HostedGate} = await import('../src/index.ts');
  const {SetupCodeService} = await import('../src/setup-code.ts');
  const gate = new HostedGate({now: () => Date.now(), random: () => 0});
  let record: import('../src/setup-code.ts').SetupCodeJournal<string> | undefined;
  const storage = {change: async (update: (value: typeof record) => NonNullable<typeof record>) => {record = structuredClone(update(structuredClone(record))); return structuredClone(record);}};
  let creates = 0, polls = 0;
  const service = new SetupCodeService('hosted-test', {
    gate,
    create: async () => {creates++; return {...authorization, deviceCode: 's'.repeat(43), verificationUri: 'https://example.com/device', interval: 5};},
    poll: async () => {polls++; throw new ApiError(400, 'authorization_pending', 'Pending.');},
    cancel: async () => {},
  }, storage, async () => crypto.randomUUID(), {requestTimeoutMs: 1000, tickMs: 1000, replaceBeforeMs: 60000, random: () => 0});
  gate.failed(120);
  await service.start();
  await advance(0);
  assert.equal(creates, 1, 'explicit start bypasses the gate');
  gate.failed(120);
  for (let n = 0; n < 3; n++) {await advance(30000); assert.equal(polls, 0); assert.equal(service.getSnapshot().userCode, authorization.userCode);}
  await advance(30000);
  assert.equal(polls, 1);
  assert.equal(service.getSnapshot().phase, 'waiting');
  assert.equal(gate.blockedFor(), 0, 'authorization_pending is normal protocol progress');
  service.pause();
});

test('slowdown never shortens a longer advertised protocol interval', async (t) => {
  const advance = clock(t);
  let polls = 0;
  const service = new DeviceAuthorizationService({
    create: async () => ({...authorization, interval: 60}),
    poll: async () => {polls++; throw new ApiError(429, 'slow_down', 'Wait.', true);},
    cancel: async () => {}, revoke: async () => {},
  }, options, async () => crypto.randomUUID());
  await service.start('TV', 'tvos');
  await advance(60000);
  await advance(30000);
  assert.equal(polls, 1);
  await advance(30000);
  assert.equal(polls, 2);
  service.cancel();
});

test('a longer sibling outage during create recovery keeps the same pending operation', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  t.mock.method(Math, 'random', () => 0);
  const api = new HttpDeviceAuthorizationApi('https://activation-extended-retry.example');
  const ids: string[] = [];
  t.mock.method(globalThis, 'fetch', async (_url: unknown, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body));
    if (body.requestId) ids.push(body.requestId);
    if (ids.length === 1) return new Response('Unavailable', {status: 503, headers: {'Retry-After': '120'}});
    return new Response(JSON.stringify(authorization));
  });
  const service = new DeviceAuthorizationService(api, options, async () => crypto.randomUUID());
  const started = service.start('TV', 'tvos');
  await settle();
  await advance(90000);
  api.gate.failed(120);
  await advance(30000);
  assert.equal(service.getSnapshot().phase, 'creating');
  assert.equal(ids.length, 1);
  await advance(90000);
  await started;
  assert.equal(ids.length, 2);
  assert.equal(ids[0], ids[1]);
  service.cancel();
});

test('cancelling while create recovery is deferred prevents a later request', async (t) => {
  const advance = clock(t);
  const {HttpDeviceAuthorizationApi} = await import('../src/index.ts');
  const api = new HttpDeviceAuthorizationApi('https://activation-cancel-retry.example');
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response('Unavailable', {status: 503, headers: {'Retry-After': '120'}}));
  const service = new DeviceAuthorizationService(api, options, async () => crypto.randomUUID());
  const started = service.start('TV', 'tvos');
  await settle();
  service.cancel();
  await started;
  await advance(180000);
  assert.equal(service.getSnapshot().phase, 'idle');
  assert.equal(fetch.mock.callCount(), 1);
});

test('the native TV journal authority automatically shares the Hosted gate after package copy', async (t) => {
  const advance = clock(t);
  const {hostedGate} = await import('../src/index.ts');
  const {SetupCodeService} = await import('../src/setup-code.ts');
  const origin = 'https://activation-native-copy.example';
  const gate = hostedGate(origin);
  t.mock.method(Math, 'random', () => 0);
  let record: import('../src/setup-code.ts').SetupCodeJournal<string> | undefined;
  const storage = {change: async (update: (value: typeof record) => NonNullable<typeof record>) => {record = structuredClone(update(structuredClone(record))); return structuredClone(record);}};
  let creates = 0, polls = 0;
  const service = new SetupCodeService(origin + '#tv-x25519-v1', {
    create: async () => {creates++; return {...authorization, deviceCode: 's'.repeat(43), interval: 5};},
    poll: async () => {polls++; throw new ApiError(400, 'authorization_pending', 'Pending.');},
    cancel: async () => {},
  }, storage, async () => crypto.randomUUID(), {requestTimeoutMs: 1000, tickMs: 1000, replaceBeforeMs: 60000, random: () => 0});
  gate.failed(120);
  await service.start();
  await advance(0);
  assert.equal(creates, 1);
  gate.failed(120);
  for (let n = 0; n < 3; n++) {await advance(30000); assert.equal(polls, 0);}
  await advance(30000);
  assert.equal(polls, 1);
  service.pause();
  gate.succeeded();
});

test('a durable TV code expires visibly during an outage while its protected receipt survives', async (t) => {
  const advance = clock(t);
  const {hostedGate} = await import('../src/index.ts');
  const {SetupCodeService} = await import('../src/setup-code.ts');
  const origin = 'https://activation-native-expiry.example', gate = hostedGate(origin);
  let record: import('../src/setup-code.ts').SetupCodeJournal<string> | undefined;
  const storage = {change: async (update: (value: typeof record) => NonNullable<typeof record>) => {record = structuredClone(update(structuredClone(record))); return structuredClone(record);}};
  let creates = 0, polls = 0;
  const service = new SetupCodeService(origin + '#tv-x25519-v1', {
    create: async () => {creates++; return {...authorization, deviceCode: 's'.repeat(43), interval: 5, expiresAt: new Date(Date.now() + 10000).toISOString()};},
    poll: async () => {polls++; return 'grant';}, cancel: async () => {},
  }, storage, async () => crypto.randomUUID(), {requestTimeoutMs: 1000, tickMs: 1000, replaceBeforeMs: 0, random: () => 0});
  await service.start(); await advance(0);
  gate.failed(120);
  await advance(10000);
  assert.equal(service.getSnapshot().userCode, undefined);
  assert.equal(creates, 1);
  assert.equal(polls, 0);
  assert.equal(record?.current?.authorization.deviceCode, 's'.repeat(43));
  service.pause(); gate.succeeded();
});
