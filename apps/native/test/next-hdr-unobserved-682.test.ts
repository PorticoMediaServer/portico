import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {localV1Http} from '../../../packages/client-core/src/playback-v1/local-http.ts';
import {PlaybackOptionsClient} from '../../../packages/client-core/src/playback-v1/options.ts';
import {parseDashboardSessionFacts} from '../../../packages/client-core/src/admin/activity-summary/parser.ts';
import {hdrText} from '../../../packages/client-core/src/presentation/kit-model/technical.ts';

// #682: hdr10Plus, dolbyVisionRpu and dolbyVisionBaseLayer arrive as `null` until observed. Through this
// app's own client-core import path, both responses still parse, the observations stay unknown (never
// "no"), and the badge falls back to the header answer: HDR10, not HDR10+.
const unobserved = {hdr10Plus: null, dolbyVisionRpu: null, dolbyVisionBaseLayer: null};
test("#682: a null-observation playback options answer parses and badges as the header says", async () => {
  const api = new HttpLocalApi("https://server.example", "", async () => new Response(JSON.stringify({itemId: "movie", kind: "movie",
    versions: [{id: "version", video: [{id: "v0", codec: "hevc", hdr: "hdr10", ...unobserved}]}]}), {status: 200, headers: {"Content-Type": "application/json"}}));
  const video = (await new PlaybackOptionsClient(localV1Http(api)).options("movie")).versions[0]!.video[0]!;
  for (const key of ["hdr10Plus", "dolbyVisionRpu", "dolbyVisionBaseLayer"] as const) assert.equal(video[key], undefined, key);
  assert.equal(hdrText(video.hdr, video.hdr10Plus, defaultI18n.t), defaultI18n.t("next.kit.hdr.hdr10"));
});
test("#682: a null-observation admin session parses instead of failing the whole sessions list", () => {
  const out = parseDashboardSessionFacts({media: {video: {id: "v0", codec: "hevc", hdr: "hdr10", ...unobserved}}, currentBitrateKbps: 0, rateWindowMs: 5000, live: false}).media as {video: Record<string, unknown>};
  assert.equal(out.video.hdr, "hdr10"); assert.equal(Object.hasOwn(out.video, "hdr10Plus"), false);
});
