import test from 'node:test';
import assert from 'node:assert/strict';
import {Outbox, OutboxWaiting, OUTBOX_MAX_AGE_MS, type OutboxAnswer, type OutboxEntry, type OutboxScope, type OutboxStore} from '../src/outbox.ts';

/**
 * The outbox against a small server that counts how often each idempotency key was applied, under
 * random sequences of: write, double tap, lost request, lost reply, busy server, client crash and
 * restart, server restart, profile switch, sign-out, sign-in again, expired sign-in and clock jump.
 *
 * It holds that every edit is applied exactly once or reported to the person once, that none is
 * ever applied under another account or profile or sent when older than 30 days, that the queue
 * ends empty, and that an idle queue writes nothing to its store. Time is virtual, so a run takes seconds. A failure prints its seed:
 * run it again alone with OUTBOX_SEED=<seed> OUTBOX_SEQUENCES=1.
 */
const SEQUENCES = Number(process.env.OUTBOX_SEQUENCES ?? 2000);
const FIRST_SEED = Number(process.env.OUTBOX_SEED ?? 20261109);
const STEPS = 40;
const DAY = 24 * 60 * 60 * 1000;

function prng(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Who = Readonly<{accountId: string; profileId: string}>;
const PEOPLE: readonly Who[] = [{accountId: 'a1', profileId: 'p1'}, {accountId: 'a1', profileId: 'p2'}, {accountId: 'a2', profileId: 'p3'}];
const scopeOf = (who: Who): OutboxScope => ({serverId: 'server', authority: 'local', ...who});
const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };

/** Virtual time: monotonic for timers, and a wall clock the sequence can make jump. */
class World {
  mono = 0;
  wall = Date.UTC(2026, 10, 9);
  private tasks: {at: number; id: number; run: () => void}[] = [];
  private nextId = 0;
  readonly scheduler = {
    now: () => this.mono,
    setTimeout: (run: () => void, delayMs: number) => { const id = ++this.nextId; this.tasks.push({at: this.mono + Math.max(0, delayMs), id, run}); return id; },
    clearTimeout: (handle: unknown) => { this.tasks = this.tasks.filter(task => task.id !== handle); },
  };
  later(delayMs: number, run: () => void) { this.scheduler.setTimeout(run, delayMs); }
  pending() { return this.tasks.length; }
  async advance(ms: number) {
    const end = this.mono + ms;
    await flush();
    for (;;) {
      let due: World['tasks'][number] | undefined;
      for (const task of this.tasks) if (task.at <= end && (!due || task.at < due.at || task.at === due.at && task.id < due.id)) due = task;
      if (!due) break;
      this.tasks = this.tasks.filter(task => task !== due);
      this.wall += Math.max(0, due.at - this.mono);
      this.mono = Math.max(this.mono, due.at);
      due.run();
      await flush();
    }
    this.wall += end - this.mono;
    this.mono = end;
  }
}

/** What the device keeps across a crash. A write is on disk at the moment its promise resolves;
 * a crash before that loses it, and the code waiting on it never runs. */
class Disk {
  entries = new Map<string, OutboxEntry>();
  writes = 0;
  accepted = new Set<string>();
  constructor(private readonly world: World) {}
  handle(alive: () => boolean): OutboxStore {
    const commit = (work: () => void) => new Promise<void>(resolve => this.world.later(0, () => { if (!alive()) return; this.writes++; work(); resolve(); }));
    return {
      list: () => new Promise(resolve => this.world.later(0, () => { if (alive()) resolve([...this.entries.values()].map(entry => structuredClone(entry))); })),
      put: entry => commit(() => { this.entries.set(entry.id, structuredClone(entry)); this.accepted.add(entry.id); }),
      delete: id => commit(() => { this.entries.delete(id); }),
    };
  }
}

type Trouble = 'busy' | 'slow-down' | 'elsewhere';
class Server {
  up = true;
  /** How many times each key's edit was applied, and for whom. */
  applied = new Map<string, {count: number; who: Who}>();
  tokens = new Map<string, Who>();
  /** Edits sent again with a token of someone other than their maker. */
  strangers: string[] = [];
  private nextToken = 0;
  signIn(who: Who) { const token = 'token-' + ++this.nextToken; this.tokens.set(token, who); return token; }
  /** Undefined: the request reached nobody. */
  handle(token: string, entry: OutboxEntry, trouble: Trouble | undefined): OutboxAnswer | undefined {
    if (!this.up) return undefined;
    if (trouble === 'busy') return {status: 503};
    if (trouble === 'elsewhere') return {status: 302}; // a proxy sends the request somewhere else
    if (trouble === 'slow-down') return {status: 429, retryAfterMs: 20_000};
    const who = this.tokens.get(token);
    if (!who) return {status: 401};
    if ((entry.body as {refuse?: boolean}).refuse) return {status: 422};
    const done = this.applied.get(entry.id);
    if (done) {
      // The same key again changes nothing, but it must still come from the person who made the edit.
      if (done.who !== who) this.strangers.push(entry.id);
      return {status: 200};
    }
    this.applied.set(entry.id, {count: 1, who});
    return {status: 200};
  }
}

type Edit = {id: string; who: Who; refuse: boolean; asking: number; refusedToCaller: number; dropped: {why: string}[]};

async function sequence(seed: number) {
  const random = prng(seed), pick = <T>(list: readonly T[]) => list[Math.floor(random() * list.length)]!;
  const world = new World(), disk = new Disk(world), server = new Server();
  const edits = new Map<string, Edit>();
  let session: {who: Who; token: string} | undefined;
  let outbox!: Outbox, life = 0;
  let loseRequest = 0, loseReply = 0, trouble: Trouble[] = [];
  let lastEdit: Edit | undefined, nextEdit = 0;
  const sentTooOld: string[] = [];

  const boot = () => {
    const mine = ++life, alive = () => mine === life;
    outbox = new Outbox({
      store: disk.handle(alive), scheduler: world.scheduler, wallNow: () => world.wall, random,
      scope: () => session && scopeOf(session.who),
      send: entry => new Promise<OutboxAnswer>((resolve, reject) => {
        const token = session!.token;
        // An edit whose caller still waits was made a moment ago, whatever a jumped clock says.
        if (world.wall - entry.createdAt > OUTBOX_MAX_AGE_MS && !edits.get(entry.id)!.asking) sentTooOld.push(entry.id);
        const lost = () => world.later(1000, () => { if (alive()) reject(new TypeError('network')); });
        if (loseRequest > 0) { loseRequest--; lost(); return; }
        world.later(50, () => {
          const answer = server.handle(token, entry, trouble.shift());
          if (!answer || loseReply > 0) { if (answer) loseReply--; lost(); return; }
          world.later(50, () => { if (alive()) resolve(answer); });
        });
      }),
      onDropped: (entry, why) => { edits.get(entry.id)!.dropped.push({why}); },
    });
    void outbox.start();
  };
  const write = (edit: Edit) => {
    const answered = outbox.add({id: edit.id, method: 'PUT', path: '/v1/items/x/personal-state', body: {refuse: edit.refuse}});
    // Two taps that are both still waiting hear the same answer on the same screen: that is one report.
    const first = edit.asking++ === 0;
    answered.then(answer => { edit.asking--; if (answer.status >= 400 && first) edit.refusedToCaller++; },
      error => { edit.asking--; assert.ok(error instanceof OutboxWaiting, String(error)); });
  };
  const signIn = (who: Who) => { session = {who, token: server.signIn(who)}; outbox.wake(); };

  boot();
  signIn(pick(PEOPLE));
  for (let step = 0; step < STEPS; step++) {
    const roll = random();
    if (roll < 0.3) {
      if (session) {
        const edit: Edit = {id: `edit-${seed}-${++nextEdit}`, who: session.who, refuse: random() < 0.1, asking: 0, refusedToCaller: 0, dropped: []};
        edits.set(edit.id, edit); lastEdit = edit; write(edit);
      }
    } else if (roll < 0.36) {
      // A second tap sends the same edit again, with the same id. (After a refusal it would be a new try, told again.)
      if (session && lastEdit && lastEdit.who === session.who && !lastEdit.refusedToCaller && !lastEdit.dropped.length) write(lastEdit);
    } else if (roll < 0.44) loseRequest++;
    else if (roll < 0.52) loseReply++;
    else if (roll < 0.58) trouble.push(pick(['busy', 'slow-down', 'elsewhere'] as const));
    else if (roll < 0.63) { server.up = false; }
    else if (roll < 0.68) { server.up = true; outbox.wake(); }
    else if (roll < 0.74) { life++; outbox.dispose(); boot(); }
    else if (roll < 0.79) signIn(pick(PEOPLE));
    else if (roll < 0.83) { if (session) server.tokens.delete(session.token); session = undefined; }
    else if (roll < 0.86) { if (session) server.tokens.delete(session.token); } // the sign-in ran out; the app has not noticed yet
    else if (roll < 0.89) world.wall += Math.round((random() - 0.3) * 3 * DAY);
    else if (roll < 0.9) world.wall += OUTBOX_MAX_AGE_MS + DAY;
    await world.advance(pick([0, 10, 200, 3000, 60_000, 600_000]));
  }

  // The network heals and each person signs in again: everything left drains.
  server.up = true; loseRequest = 0; loseReply = 0; trouble = [];
  for (const who of PEOPLE) { signIn(who); await world.advance(2 * 60 * 60 * 1000); }

  for (const edit of edits.values()) {
    if (!disk.accepted.has(edit.id)) continue; // the app died before the edit reached the device's storage
    const applied = server.applied.get(edit.id), reports = edit.refusedToCaller + edit.dropped.length;
    assert.ok((applied?.count ?? 0) <= 1, `${edit.id} was applied ${applied?.count} times`);
    assert.ok(reports <= 1, `${edit.id} was reported ${reports} times`);
    // Applied and also reported happens one way only: the reply was lost, then the edit grew too old to ask again.
    if (applied && reports) assert.deepEqual(edit.dropped, [{why: 'expired'}], `${edit.id} was applied and reported as refused`);
    assert.ok(applied || reports, `${edit.id} was neither applied nor reported`);
    if (applied) assert.deepEqual(applied.who, edit.who, `${edit.id} was applied under another account or profile`);
  }
  assert.deepEqual(server.strangers, [], 'an edit is never sent again under another account or profile');
  assert.deepEqual(sentTooOld, [], 'an edit older than 30 days is dropped, not sent');
  assert.equal(disk.entries.size, 0, 'the queue is empty at the end');
  assert.equal(outbox.waiting(), 0);

  const writes = disk.writes;
  await world.advance(60 * 60 * 1000);
  assert.equal(disk.writes, writes, 'an idle queue writes nothing');
  assert.equal(world.pending(), 0, 'an idle queue keeps no timer');
  outbox.dispose();
}

test(`outbox model: ${SEQUENCES} random sequences end with every edit applied once or reported once`, async () => {
  for (let n = 0; n < SEQUENCES; n++) {
    const seed = FIRST_SEED + n;
    try { await sequence(seed); } catch (error) { throw new Error(`outbox model failed with OUTBOX_SEED=${seed}: ${(error as Error).message}`, {cause: error}); }
  }
});
