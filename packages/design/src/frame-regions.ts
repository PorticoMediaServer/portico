/**
 * Each frame's required regions, as data (Stage 1 round 8): the regions a kind of screen draws across all its mock-up
 * frames and states, which forms draw each, and the mock-up frames it comes from. Both apps' frames take exactly these
 * regions as props, with these names; a region a screen does not fill is absent, never a blank. One key per frame.
 *
 * Forms are frames.ts's (`phone`, `medium`, `expanded`, `tv`), plus `web` for the expanded form in a browser where it
 * differs from a tablet's. `flexible` marks the frame's one flexible region.
 */
import type {FrameForm} from './frames.ts';

export type RegionForm = FrameForm | 'web';
export type FrameRegion = Readonly<{id: string; forms: readonly RegionForm[]; from: readonly string[]; flexible?: true}>;
const all: readonly RegionForm[] = ['phone', 'medium', 'expanded', 'web', 'tv'];
const touch: readonly RegionForm[] = ['phone', 'medium', 'expanded', 'web'];
const wide: readonly RegionForm[] = ['medium', 'expanded', 'web'];

export const frameRegions = {
  /** Frame 7, the video player (PlayerChromeFrame). Its parts are kit/player's. */
  player: [
    {id: 'picture', forms: all, from: ['desktop-player f1', 'tablet-play f1', 'phone-player r1', 'tv-player p1']},
    {id: 'close', forms: touch, from: ['desktop-player f1', 'phone-player r1']},
    {id: 'title', forms: all, from: ['desktop-player f1 (episode line, "Paused at" f6)', 'desktop-live live-player', 'tv-player p11']},
    {id: 'tools', forms: all, from: ['desktop-player f1 (Play on, More, Close)', 'phone-player r1 (Play on, PiP, options)', 'tablet-play f8 (Record, More, Close)']},
    {id: 'scrubber', forms: all, from: ['desktop-player f1 (recap, intro, credits tinted)', 'tv-player p2 (filmstrip, chapter names)', 'phone-player 1b (preview)']},
    {id: 'times', forms: all, from: ['desktop-player f1 (Ends 4:48 PM)', 'phone-player 1c (behind live)', 'tv-player p1 (clock)']},
    {id: 'transport', forms: all, from: ['desktop-player f1', 'phone-player r1, r4', 'tv-player p1, p11 (channel steps)']},
    {id: 'leading', forms: all, from: ['desktop-player f1 (Episodes)', 'desktop-live live-player (Guide)']},
    {id: 'trailing', forms: all, from: ['desktop-player f1 (volume, subtitles, quality, speed, PiP, full screen)', 'phone-player r1 (Subtitles, Audio, quality pills)']},
    {id: 'prompts', forms: all, from: ['desktop-player f1, f2s (Skip intro, Skip recap, Skipped + Undo)', 'phone-player 2, 2b', 'tablet-play f8 (Go to live)']},
    {id: 'card', forms: all, from: ['desktop-player f4, f4s (Up next, Skip credits, scene note, countdown)', 'phone-player 6, 6b, 6c, 6e (Still watching?)', 'tv-player p5, p6']},
    {id: 'panel', forms: all, from: ['desktop-player f3, f6 (options, Episodes drawer)', 'tablet-play f2 (beside)', 'phone-player 3 (sheet, side sheet)', 'tv-player p4 (in place)', 'desktop-live live-player (short guide)']},
    {id: 'hints', forms: ['tv'], from: ['tv-player p2', 'tv-player p4 (Back Close)']},
    {id: 'body', forms: ['phone', 'medium'], from: ['phone-player r4 (watch page)', 'tablet-play f3', 'phone-player 1c (channel page under an upright live picture)'], flexible: true},
  ],
  /** Frame 8, the audio player (AudioPlayerFrame). Its parts are kit/player's (queue.tsx on native, Queue.tsx on the web). */
  audio: [
    {id: 'close', forms: touch, from: ['desktop-listen now', 'phone-app 14', 'tablet-play f9']},
    {id: 'context', forms: all, from: ['desktop-listen now ("Playing from After Hours")', 'final3-tv-12']},
    {id: 'device', forms: touch, from: ['desktop-listen now (top right)', 'tablet-play f10 (pill row)', 'phone-app 14 (bottom row)']},
    {id: 'more', forms: all, from: ['desktop-listen now', 'final3-tv-12 (button row)']},
    {id: 'notice', forms: wide, from: ['desktop-listen book-player ("Went back 30 s")', 'tablet-play f12']},
    {id: 'artwork', forms: all, from: ['desktop-listen now', 'phone-app 14, 15', 'final3-tv-12']},
    {id: 'title', forms: all, from: ['desktop-listen now', 'final3-tv-12 (display line)']},
    {id: 'badge', forms: wide, from: ['desktop-listen book-player (Unabridged)']},
    {id: 'byline', forms: all, from: ['desktop-listen now (artist, album)', 'book-player (author, Read by)', 'final3-tv-12 (album, year)']},
    {id: 'favourite', forms: touch, from: ['desktop-listen now', 'phone-app 14']},
    {id: 'titleMore', forms: wide, from: ['desktop-listen now (More by the heart)', 'tablet-play f9']},
    {id: 'chapter', forms: all, from: ['desktop-listen book-player', 'phone-app 15']},
    {id: 'scrubber', forms: all, from: ['desktop-listen now', 'book-player (segmented by chapter)']},
    {id: 'times', forms: all, from: ['desktop-listen now (at the ends)', 'phone-app 14, 15 (under)', 'final3-tv-12']},
    {id: 'transport', forms: all, from: ['desktop-listen now (compact)', 'phone-app 14', 'final3-tv-12']},
    {id: 'volume', forms: ['web'], from: ['desktop-listen now', 'book-player']},
    {id: 'next', forms: ['phone'], from: ['phone-app 14']},
    {id: 'lyrics', forms: ['phone', 'medium', 'tv'], from: ['phone-app 14', 'tablet-play f10', 'final3-tv-12']},
    {id: 'queue', forms: ['phone', 'medium'], from: ['phone-app 14', 'tablet-play f10']},
    {id: 'playlist', forms: ['tv'], from: ['final3-tv-12 (Add to playlist)']},
    {id: 'speed', forms: all, from: ['phone-app 15 (tile)', 'desktop-listen book-player (row of choices)']},
    {id: 'sleep', forms: all, from: ['phone-app 15 (tile)', 'desktop-listen book-player (row of choices)']},
    {id: 'chapters', forms: ['phone', 'tv'], from: ['phone-app 15 (tile)']},
    {id: 'panel', forms: all, from: ['desktop-listen now, now-states (Up next / Lyrics)', 'book-player (Chapters / Up next)', 'tablet-play f9, f10, f12', 'final3-tv-12', 'select-18 (phone sheet)'], flexible: true},
  ],
} as const satisfies Readonly<Record<string, readonly FrameRegion[]>>;
export type FrameRegions = typeof frameRegions;
