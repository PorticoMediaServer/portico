import {formatMessage} from '@i18n';
import {livePlayerFixtureMessages} from '../../../packages/i18n/src/catalog/en-US/next/dev/live-player-fixtures';
import type {LiveChromePort} from '../kit/player';

/** The drawn live schedule for the Live player's dev specimen and its Kit cases; no server data or artwork. */
const fixture = (key: keyof typeof livePlayerFixtureMessages) => formatMessage(livePlayerFixtureMessages[key], {}, {locale: 'en-US'});
const at = (hour: number, minute: number) => Date.UTC(2026, 9, 21, hour + 3, minute);
const now = at(16, 5);
const mark = (text: string, seed: string) => ({text, seed});
const channel = (id: string, number: string, name: string, markText: string, seed: string) => ({id, sourceId: 'synthetic', kind: 'live-source' as const,
  generation: `gen-${id}`, number, name, mark: mark(markText, seed), favorite: false, recordAvailable: true, tuneAvailable: true});
const sse = channel('sse', '202', fixture('next.live.fixture.sportsExtra'), 'SSE', 'synthetic:sse');
const ss = channel('ss', '201', fixture('next.live.fixture.sports'), 'SS', 'synthetic:ss');
const lm = channel('lm', '301', fixture('next.live.fixture.movies'), 'LM', 'synthetic:lm');
const lc = channel('lc', '302', fixture('next.live.fixture.classics'), 'LC', 'synthetic:lc');
const pk = channel('pk', '401', fixture('next.live.fixture.kids'), 'PK', 'synthetic:pk');
const programme = (id: string, title: string, startMs: number, endMs: number, extra: Record<string, unknown> = {}) => ({id, title, startMs, endMs, ...extra});
const current = programme('cycling', fixture('next.live.fixture.cycling'), at(14, 0), at(17, 0), {subtitle: fixture('next.live.fixture.stage'), episode: {season: 5, number: 9}});
const rows = [
  {channel: ss, programme: programme('cycling-ss', fixture('next.live.fixture.cycling'), at(14, 0), at(18, 30)), nextStartMs: at(18, 30)},
  {channel: sse, programme: current, nextStartMs: at(17, 0)},
  {channel: lm, programme: programme('glass', fixture('next.live.fixture.glass'), at(13, 57), at(16, 11)), nextStartMs: at(16, 11)},
  {channel: lc, programme: programme('keeper', fixture('next.live.fixture.keeper'), at(15, 54), at(17, 52)), nextStartMs: at(17, 52)},
  {channel: pk, programme: programme('story', fixture('next.live.fixture.story'), at(16, 0), at(16, 30), {subtitle: fixture('next.live.fixture.homecoming'), episode: {season: 3, number: 1}}), nextStartMs: at(16, 30)},
];
export const livePlayerPort: LiveChromePort = {fence: {} as never, channel: sse, programme: current,
  nextProgramme: {title: fixture('next.live.fixture.cycling'), startMs: at(17, 0), endMs: at(17, 30)},
  window: {mediaOriginMs: at(15, 35), programmeStartMs: at(14, 0), programmeEndMs: at(17, 0), seekStartMs: at(15, 35), seekEndMs: now, liveEdgeMs: now,
    positionMs: at(16, 3), behindSeconds: 120, atLive: false},
  down: rows[0], up: rows[2], miniGuide: rows, skipBackSeconds: 10, skipForwardSeconds: 30,
  isCurrent: () => true, record: async () => {}, goLive: async () => {}, tune: async () => {}, tuneTo: async () => {},
  openFullGuide: () => {}, openRecordings: () => {}};
