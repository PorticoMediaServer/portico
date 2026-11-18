/**
 * Each frame's required regions, written once (Stage 1 round 8). A region is a place the frame provides: its `id` is the
 * frame's prop of that name in both apps (a dotted id is a field of that prop), `forms` are the forms that draw it (a form
 * that does not draw it has no place for it), and `from` names the mock-up frames and states it comes from (design/ui-v1).
 * `flexible` marks the frame's flexible region (a frame fills one of them). `drawnBy` names a region the frame keeps room
 * for but another part draws: the shell's bar, or R1's start focus. A region a screen does not fill is absent, never a blank.
 *
 * One key per frame; each family owns its key. FRAMES.md beside each app's frames says how each region is drawn per form.
 */
import type {FrameForm} from './frames.ts';

export type FrameRegion = Readonly<{id: string; forms: readonly FrameForm[]; from: readonly string[]; flexible?: true; drawnBy?: 'shell' | 'R1'}>;
const every: readonly FrameForm[] = ['phone', 'medium', 'expanded', 'tv'];
const touch: readonly FrameForm[] = ['phone', 'medium', 'expanded'];

export const frameRegions = {
  /** `BrowseFrame` with a `header`: a library, and any page of rows or a grid under a title (See all, a collection's grid). */
  browse: [
    {id: 'header.title', forms: every, from: ['desktop-browse grid', 'phone-app 4, 5', 'tablet 3, 4, 5', 'final3-tv-08']},
    {id: 'header.switcher', forms: every, from: ['desktop-browse grid (the round chevron)', 'phone-app 4, 4b (the centred name)', 'tablet 4', 'final3-tv-08 (title ▾)']},
    {id: 'header.back', forms: touch, from: ['phone-app 4 (‹ Libraries)', 'collections-46, -47 (See all)']},
    {id: 'header.trailing', forms: ['medium', 'expanded'], from: ['tablet 3, 4 (Select)', 'desktop-listen music (Shuffle)']},
    {id: 'header.menu', forms: touch, from: ['phone-app 4c', 'desktop-browse grid (⋯)', 'tablet 3']},
    {id: 'header.intro', forms: touch, from: ['desktop-browse seeall (why these titles are here)', 'collections-46, -47']},
    {id: 'header.actions', forms: touch, from: ['desktop-browse seeall (Play, Shuffle)', 'collections-46, -47']},
    {id: 'header.tabs', forms: every, from: ['desktop-browse grid', 'phone-app 4', 'tablet 4', 'final3-tv-08', 'desktop-listen music (Discover / Artists / Albums / Songs / Genres)']},
    {id: 'toolbar', forms: every, from: ['desktop-browse grid, filters, sort', 'phone-app 5', 'tablet 4', 'final3-tv-08', 'tv-focus 18-21']},
    {id: 'filters', forms: every, from: ['desktop-browse filters (side panel)', 'phone-app 6 (sheet)', 'tablet 4 (docked), 5 (over the grid)', 'final3-tv-08 (side sheet)']},
    {id: 'grid', forms: every, flexible: true, from: ['desktop-browse grid, big, list, collections', 'phone-app 5', 'tablet 4, 5', 'final3-tv-08']},
    {id: 'alphabet', forms: touch, from: ['desktop-browse grid, big (rail and the scroll bubble)', 'phone-app 5', 'tablet 4']},
    {id: 'children', forms: every, flexible: true, from: ['phone-app 4 (Discover: Continue Watching, Recently added ›, Recommended)', 'desktop-browse recommended, seeall', 'final3-tv-08']},
    {id: 'state', forms: every, from: ['system-rules 12 (loading keeps the loaded page\'s boxes)', 'states-22b', 'desktop-browse empty']},
    {id: 'startFocus', forms: ['tv'], drawnBy: 'R1', from: ['tv-focus 18 (the first card; never the rail)']},
  ],
  /** `BrowseFrame` with a `hero`: Home. */
  home: [
    {id: 'bar', forms: ['phone'], drawnBy: 'shell', from: ['phone-app 1, 2 (wordmark, Cast, avatar; solid once scrolled)', 'final-mobileweb-1 frame 1']},
    {id: 'hero', forms: every, from: ['mock.html home', 'final-desktop-1 (pager thumbnails)', 'phone-app 1', 'tablet 1, 2', 'final3-tv-01 (follows focus; owner, 18 Nov)']},
    {id: 'hero.hints', forms: ['tv'], from: ['final3-tv-01 (OK Resume, Hold OK More options)']},
    {id: 'hero.focusedEntry', forms: ['tv'], from: ['final3-tv-01, tv-focus 2 (the hero shows the focused card)']},
    {id: 'children', forms: every, flexible: true, from: ['phone-app 1, 2 (Continue Watching, New on your server ›, On now, Continue Listening, Popular)', 'mock.html home', 'tablet 1, 2', 'final3-tv-01']},
    {id: 'state', forms: every, from: ['system-rules 12', 'states 4b']},
    {id: 'startFocus', forms: ['tv'], drawnBy: 'R1', from: ['owner, 18 Nov: the first Continue Watching card']},
  ],
} as const satisfies Readonly<Record<string, readonly FrameRegion[]>>;
export type FrameRegionKey = keyof typeof frameRegions;
