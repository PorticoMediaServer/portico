import { test } from "node:test";
import assert from "node:assert/strict";
import { PlaybackService, ViewerService, HttpLocalApi } from "../src/index.ts";
import type { PlaybackApi, PlaybackSession } from "../src/index.ts";
const session: PlaybackSession = {
  id: "s",
  generation: 1,
  streamUrl: "/v1/media/opaque",
  mode: "direct",
  duration: 60,
  resumeSeconds: 2,
};
const deferred = <T>() => {
  let resolve!: (v: T) => void;
  let reject!: (v: unknown) => void;
  const promise = new Promise<T>((a, b) => {
    resolve = a;
    reject = b;
  });
  return { promise, resolve, reject };
};
function api(
  create: () => Promise<PlaybackSession> = async () => session,
  stop: (id: string) => Promise<void> = async () => {}
): PlaybackApi {
  return {
    createPlayback: create,
    stopPlayback: stop,
    progressPlayback: async () => {},
  };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));
test("pause before server and native ready remains paused", async () => {
  const create = deferred<PlaybackSession>();
  const service = new PlaybackService(
    api(() => create.promise),
    () => "r"
  );
  const work = service.play("movie");
  service.pause();
  create.resolve(session);
  await work;
  service.ready(service.getSnapshot().intentId);
  assert.equal(service.getSnapshot().phase, "ready");
  assert.equal(service.getSnapshot().intent, "paused");
  service.fact(service.getSnapshot().intentId, 3, "playing");
  assert.equal(service.getSnapshot().intent, "paused");
});
test("leave is immediate even when remote stop never resolves", async () => {
  const service = new PlaybackService(
    api(undefined, () => new Promise(() => {})),
    () => "r"
  );
  await service.play("movie");
  service.leave();
  assert.equal(service.getSnapshot().phase, "idle");
  assert.equal(service.getSnapshot().session, undefined);
  assert.equal(service.getSnapshot().cleanupPending, 1);
});
test("late create is cleaned up and never replaces next intent", async () => {
  const create = deferred<PlaybackSession>();
  const stops: string[] = [];
  const service = new PlaybackService(
    api(
      () => create.promise,
      async (id) => {
        stops.push(id);
      }
    ),
    () => "r"
  );
  const work = service.play("movie");
  await tick();
  service.leave();
  create.resolve(session);
  await work;
  await tick();
  assert.deepEqual(stops, ["s"]);
  assert.equal(service.getSnapshot().phase, "idle");
});
test("failed cleanup persists for explicit reconciliation", async () => {
  let attempts = 0;
  const service = new PlaybackService(
    api(undefined, async () => {
      if (++attempts === 1) throw Error("offline");
    }),
    () => "r"
  );
  await service.play("movie");
  service.leave();
  await tick();
  assert.equal(service.getSnapshot().cleanupPending, 1);
  service.retryCleanup();
  await tick();
  assert.equal(service.getSnapshot().cleanupPending, 0);
  assert.equal(attempts, 2);
});
test("LAN authority survives internet/hosted loss", () => {
  const viewer = new ViewerService();
  viewer.select("http://127.0.0.1:19411", {
    accessToken: "fixture",
    expiresAt: "2099-01-01T00:00:00Z",
    sessionFamilyId: "family", tokenGeneration: "1", authorizationHorizon: "2099-01-01T01:00:00Z",
    viewer: {
      accountId: "a",
      profileId: "p",
      serverId: "s",
      authority: "local",
      role: "owner",
    },
  });
  const generation = viewer.getSnapshot().generation;
  viewer.setConnectivity(false, false);
  assert.equal(viewer.getSnapshot().phase, "ready");
  assert.equal(viewer.getSnapshot().generation, generation);
});
test("media URLs stay on the selected server's origin", async () => {
  const fake = async () => new Response("{}", { status: 200 });
  const client = new HttpLocalApi(
    "http://127.0.0.1:19411",
    "token",
    fake as typeof fetch
  );
  // Playback is started, reported and stopped only through Playback Protocol v1 (V1PlaybackApi).
  for (const gone of ["createPlayback", "progressPlayback", "stopPlayback", "reportRouteFailure"]) assert.equal(gone in client, false, gone);
  assert.equal(
    client.mediaUrl("/v1/media/opaque"),
    "http://127.0.0.1:19411/v1/media/opaque"
  );
  assert.throws(() => client.mediaUrl("https://other.example/secret"));
});
test("a farewell is sent at once with keepalive and the token in hand, and only to the server's own API", async () => {
  const sent: {url: string; init: RequestInit}[] = [];
  const fake = async (url: any, init: any) => { sent.push({url: String(url), init}); return new Response(null, { status: 204 }); };
  const client = new HttpLocalApi("http://127.0.0.1:19411", "token", fake as typeof fetch);
  client.farewell("/v1/playback/sessions/s1", "DELETE", { positionMs: 1200 });
  client.farewell("/elsewhere", "DELETE");
  assert.equal(sent.length, 1);
  assert.equal(sent[0]!.url, "http://127.0.0.1:19411/v1/playback/sessions/s1");
  assert.equal(sent[0]!.init.keepalive, true);
  assert.equal(sent[0]!.init.method, "DELETE");
  assert.equal((sent[0]!.init.headers as Record<string, string>).Authorization, "Bearer token");
  assert.equal(JSON.parse(String(sent[0]!.init.body)).positionMs, 1200);
});
test("pause and leave checkpoint the final fractional position", async () => {
  const checkpoints: any[] = [];
  const client = api();
  client.progressPlayback = async (_id, p) => {
    checkpoints.push(p);
  };
  const service = new PlaybackService(client, () => "r");
  await service.play("m");
  service.seekApplied(service.getSnapshot().intentId, service.getSnapshot().pendingSeek!.revision, 2.4, "playing");
  service.pause();
  await tick();
  service.leave();
  await tick();
  assert.equal(checkpoints.at(-1).positionSeconds, 2.4);
  assert.equal(checkpoints.at(-1).state, "paused");
});
test("prepared session that never becomes ready times out and cleans up", async () => {
  const stopped: string[] = [];
  const service = new PlaybackService(
    api(undefined, async (id) => {
      stopped.push(id);
    }),
    () => "r",
    { startupTimeoutMs: 5 }
  );
  await service.play("m");
  await new Promise((r) => setTimeout(r, 15));
  await tick();
  assert.equal(service.getSnapshot().phase, "error");
  assert.equal(service.getSnapshot().session, undefined);
  assert.deepEqual(stopped, ["s"]);
});
test("journal restores cleanup including final checkpoint", async () => {
  const stopped: string[] = [];
  const writes: any[] = [];
  const journal = {
    load: async () => [
      {
        id: "old",
        checkpoint: {
          generation: 2,
          sequence: 7,
          positionSeconds: 9,
          state: "paused" as const,
        },
      },
    ],
    save: async (entries: any) => {
      writes.push(entries);
    },
  };
  new PlaybackService(
    api(undefined, async (id) => {
      stopped.push(id);
    }),
    () => "r",
    { journal }
  );
  await tick();
  assert.deepEqual(stopped, ["old"]);
  assert.deepEqual(writes.at(-1), []);
});
test("unresolved creation is journaled and replayed using the exact receipt", async () => {
  let count = 0;
  const receipts: string[] = [];
  const client = api();
  client.createPlayback = async (_item, requestId) => {
    receipts.push(requestId);
    if (++count === 1) throw Error("connection lost after commit");
    return session;
  };
  const records: any[] = [];
  const service = new PlaybackService(client, () => "same-receipt", {
    journal: {
      load: async () => [],
      save: async (x) => {
        records.push(x);
      },
    },
  });
  await service.play("m");
  assert.ok(
    records.some((x) =>
      x.some((r: any) => r.request?.requestId === "same-receipt")
    )
  );
  service.leave();
  service.retryCleanup();
  await tick();
  assert.deepEqual(receipts, ["same-receipt", "same-receipt"]);
  assert.equal(service.getSnapshot().cleanupPending, 0);
});
test("media readiness uses a separate nonfatal resume deadline", async () => {
  const service = new PlaybackService(api(), () => "r", {
    startupTimeoutMs: 5,
    seekTimeoutMs: 5,
  });
  await service.play("m");
  service.ready(service.getSnapshot().intentId);
  await new Promise((r) => setTimeout(r, 15));
  assert.equal(service.getSnapshot().phase, "ready");
  assert.equal(service.getSnapshot().intent, "paused");
  assert(service.getSnapshot().seekError);
  service.leave();
});
test("intentional pause at ready never times out", async () => {
  const service = new PlaybackService(api(), () => "r", {
    startupTimeoutMs: 5,
  });
  await service.play("m");
  service.pause();
  service.ready(service.getSnapshot().intentId);
  await new Promise((r) => setTimeout(r, 15));
  assert.equal(service.getSnapshot().phase, "ready");
  service.leave();
});
test('default fetch preserves the global receiver required by browser fetch',async()=>{const original=globalThis.fetch;globalThis.fetch=async function(this:unknown){assert.equal(this,globalThis);return new Response(JSON.stringify({id:'s'}));} as typeof fetch;try{await new HttpLocalApi('http://127.0.0.1:19411').system();}finally{globalThis.fetch=original;}});
test('server-completed movie starts at its supplied zero and Play after ended is a fresh intent',async()=>{let creates=0;const client=api(async()=>({...session,id:'s'+(++creates),resumeSeconds:0}));const service=new PlaybackService(client,()=>String(creates));await service.play('m');assert.equal(service.getSnapshot().session?.resumeSeconds,0);service.seekApplied(service.getSnapshot().intentId,service.getSnapshot().pendingSeek!.revision,0,'paused');service.fact(service.getSnapshot().intentId,60,'ended');service.resume();await tick();assert.equal(creates,2);assert.equal(service.getSnapshot().session?.resumeSeconds,0);service.leave();});

test("ended playback resumes at an explicit later seek target", async () => {
  let request = 0;
  const service = new PlaybackService(api(async () => ({...session,id: String(++request)})), () => String(request));
  await service.play("movie");
  service.seekApplied(service.getSnapshot().intentId,service.getSnapshot().pendingSeek!.revision,service.getSnapshot().pendingSeek!.positionSeconds,'paused');
  service.fact(service.getSnapshot().intentId, 60, "ended");
  service.seek(9);
  service.resume();
  await tick();
  assert.equal(service.getSnapshot().pendingSeek?.positionSeconds, 9);
  assert.equal(service.getSnapshot().positionSeconds, 0);
  assert.equal(service.getSnapshot().intent, "playing");
  assert.equal(request, 2);
  service.seekApplied(service.getSnapshot().intentId,service.getSnapshot().pendingSeek!.revision,9,'paused');
  service.fact(service.getSnapshot().intentId, 60, "ended");
  service.resume();
  await tick();
  assert.equal(service.getSnapshot().positionSeconds, 0);
  service.leave();
});


test("near-end audiobook server bookmark survives paused startup and adapter delivery", async () => {
  const create = deferred<PlaybackSession>();
  const service = new PlaybackService(api(() => create.promise), () => "book-request");
  const positions: number[] = [];
  service.attachAdapter({ apply: (snapshot) => { if (snapshot.session) positions.push(snapshot.pendingSeek?.positionSeconds ?? snapshot.positionSeconds); } });
  const work = service.play("audiobook-file");
  service.pause();
  create.resolve({ ...session, duration: 600, resumeSeconds: 599.25 });
  await work;
  assert.equal(service.getSnapshot().phase, "starting");
  assert.equal(service.getSnapshot().session?.resumeSeconds, 599.25);
  assert.equal(service.getSnapshot().pendingSeek?.positionSeconds, 599.25);
  assert.equal(service.getSnapshot().positionSeconds, 0);
  service.ready(service.getSnapshot().intentId);
  assert.equal(service.getSnapshot().phase, "ready");
  assert.equal(service.getSnapshot().intent, "paused");
  assert.equal(positions.at(-1), 599.25);
  service.leave();
});

test("explicit start-over zero overrides a near-end server bookmark", async () => {
  const service = new PlaybackService(api(async () => ({ ...session, duration: 600, resumeSeconds: 599.25 })), () => "start-over");
  await service.play("audiobook-file", 0);
  assert.equal(service.getSnapshot().session?.resumeSeconds, 0);
  assert.equal(service.getSnapshot().positionSeconds, 0);
  service.ready(service.getSnapshot().intentId);
  assert.equal(service.getSnapshot().intent, "playing");
  service.leave();
});
