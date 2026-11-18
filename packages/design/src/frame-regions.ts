/**
 * Each frame's required regions, as data (R8 spec, "One architecture"). One key per frame: each region's id, the forms
 * that draw it and the mock-up frames it comes from. Both apps' frames take exactly these regions as props; a region a
 * screen does not fill is absent, never a blank. `flexible` marks the frame's one flexible region.
 *
 * A NAMED USE is a kind of screen drawn on an existing frame: its key names the frame (`uses`) and lists the regions of
 * that frame it fills, so a screen built on it knows what it must provide.
 *
 * Mock-up names are `design/ui-v1` pages (`desktop-search s-all` is desktop-search.html's frame `s-all`; `phone-app 7` is the
 * seventh phone drawn on phone-app.html, in its row r3) or `shots/`.
 */
import type {FrameForm} from './frames.ts';

export type FrameRegion = Readonly<{id: string; forms: readonly FrameForm[]; from: readonly string[]; flexible?: true}>;
export type FrameRegions = Readonly<{frame: string; uses?: string; regions: readonly FrameRegion[]}>;

const all = ['phone', 'medium', 'expanded', 'tv'] as const satisfies readonly FrameForm[];
const touch = ['phone', 'medium', 'expanded'] as const satisfies readonly FrameForm[];
const wide = ['medium', 'expanded'] as const satisfies readonly FrameForm[];

export const frameRegions = {
  // Family F: the kinds of screen that had no frame (round 8).
  search: {frame: 'SearchFrame', regions: [
    {id: 'title', forms: ['phone'], from: ['phone-app 7']},
    {id: 'field', forms: all, from: ['desktop-search s-empty', 'phone-app 7', 'phone-app 8', 'tablet 10', 'final3-tv-09-search']},
    {id: 'cancel', forms: ['phone'], from: ['phone-app 8']},
    {id: 'keyboard', forms: ['tv'], from: ['final3-tv-09-search', 'tv-focus 22']},
    {id: 'scopes', forms: all, from: ['desktop-search s-all', 'desktop-search s-top', 'phone-app 8', 'tablet 10', 'final3-tv-09-search']},
    {id: 'notice', forms: touch, from: ['desktop-search s-top']},
    {id: 'none', forms: all, from: ['desktop-search s-none']},
    {id: 'results', forms: all, flexible: true, from: ['desktop-search s-empty', 'desktop-search s-all', 'desktop-search s-songs', 'desktop-search s-top', 'phone-app 7', 'phone-app 8', 'final3-tv-09-search']},
  ]},
  live: {frame: 'LiveFrame', regions: [
    {id: 'title', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'switcher', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'final3-tv-10-live-guide']},
    {id: 'facts', forms: ['expanded'], from: ['desktop-live live-guide']},
    {id: 'actions', forms: all, from: ['phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'tabs', forms: wide, from: ['desktop-live live-guide', 'desktop-live live-upcoming', 'tablet-play 6']},
    {id: 'chips', forms: all, from: ['desktop-live live-guide', 'phone-guide g1', 'tablet-play 6', 'final3-tv-10-live-guide']},
    {id: 'lead', forms: ['expanded'], from: ['desktop-live live-guide']},
    {id: 'toolbar', forms: touch, from: ['desktop-live live-guide', 'desktop-live live-upcoming', 'phone-guide g1', 'tablet-play 6']},
    {id: 'content', forms: all, flexible: true, from: ['desktop-live live-guide', 'desktop-live live-list', 'desktop-live live-upcoming', 'desktop-live live-recorded', 'desktop-live live-rules', 'phone-guide g1', 'phone-guide g2']},
    {id: 'details', forms: all, from: ['desktop-live live-sheet', 'desktop-live live-rules', 'phone-guide g3', 'tablet-play 6', 'tablet-play 7', 'final3-tv-10-live-guide']},
    {id: 'hints', forms: ['expanded', 'tv'], from: ['desktop-live live-guide']},
  ]},
  split: {frame: 'SplitFrame', regions: [
    {id: 'page', forms: all, flexible: true, from: ['tablet-play 6', 'tablet 11b']},
    {id: 'pane', forms: all, from: ['desktop-live live-sheet', 'desktop-live live-rules', 'phone-guide g3', 'tablet-play 6', 'tablet-play 7', 'tablet 11b', 'final3-tv-10-live-guide']},
  ]},
  reader: {frame: 'ReaderFrame', regions: [
    {id: 'bar', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5', 'tablet-read 4']},
    {id: 'tools', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5', 'tablet-read 4']},
    {id: 'page', forms: touch, flexible: true, from: ['desktop-read reader', 'desktop-read comic', 'desktop-read pdf', 'phone-read r5', 'tablet-read 5']},
    {id: 'turns', forms: ['expanded'], from: ['desktop-read reader-chrome']},
    {id: 'progress', forms: touch, from: ['desktop-read reader-chrome', 'phone-read r5']},
    {id: 'panel', forms: touch, from: ['desktop-read panel', 'desktop-read search', 'tablet-read 7', 'tablet-read 7b', 'phone-read r6']},
    {id: 'display', forms: touch, from: ['desktop-read aa', 'tablet-read 6', 'tablet-read 6b', 'phone-read r6']},
  ]},
  saved: {frame: 'BrowseFrame', uses: 'BrowseFrame', regions: [
    {id: 'header.title', forms: all, from: ['desktop-search saved', 'phone-app 9', 'tablet 11']},
    {id: 'header.tabs', forms: all, from: ['desktop-search saved', 'desktop-search history', 'tablet 11', 'collections-15-tv-saved-playlists']},
    {id: 'header.toolbar', forms: touch, from: ['desktop-search saved', 'tablet 11']},
    {id: 'grid', forms: all, flexible: true, from: ['desktop-search saved', 'tablet 11']},
  ]},
  downloads: {frame: 'BrowseFrame', uses: 'SplitFrame + BrowseFrame', regions: [
    {id: 'header.title', forms: touch, from: ['collections-34-desktop-downloads', 'tablet 11b']},
    {id: 'header.tabs', forms: wide, from: ['collections-34-desktop-downloads', 'tablet 11b']},
    {id: 'rows', forms: touch, flexible: true, from: ['collections-34-desktop-downloads', 'collections-36-mobile-web-downloads', 'tablet 11b']},
    {id: 'pane', forms: ['medium', 'expanded'], from: ['tablet 11b']},
  ]},
  music: {frame: 'BrowseFrame', uses: 'BrowseFrame', regions: [
    {id: 'header.title', forms: all, from: ['final3-desktop-12-music-discover', 'desktop-listen music', 'phone-app 15a']},
    {id: 'header.switcher', forms: all, from: ['final3-desktop-12-music-discover']},
    {id: 'header.trailing', forms: touch, from: ['final3-desktop-12-music-discover']},
    {id: 'header.tabs', forms: all, from: ['final3-desktop-12-music-discover', 'desktop-listen songs']},
    {id: 'rows', forms: all, flexible: true, from: ['final3-desktop-12-music-discover', 'desktop-listen songs', 'phone-app 15a', 'final3-tv-11-music-card']},
  ]},
  audiobooks: {frame: 'BrowseFrame', uses: 'BrowseFrame', regions: [
    {id: 'header.title', forms: all, from: ['desktop-listen books', 'phone-app 15c', 'listen-7-audiobooks-discover']},
    {id: 'header.switcher', forms: all, from: ['desktop-listen books']},
    {id: 'header.tabs', forms: all, from: ['desktop-listen books']},
    {id: 'rows', forms: all, flexible: true, from: ['desktop-listen books', 'phone-app 15c']},
  ]},
} as const satisfies Readonly<Record<string, FrameRegions>>;
