/**
 * Portico's one icon registry (PC-VISUAL §19, A11Y-12.5, X-17, CON-15/16).
 *
 * Every glyph is on a 24×24 grid, stroked at 1.75 with round caps and joins,
 * Lucide-derived. Each entry carries its one `meaning`: an icon is chosen by
 * what it means, never because it looks close enough. Web and Apple render
 * from this data (`ui/Icon.tsx` on each platform), so the platforms can't
 * drift, and `IconId` is a closed union, so an unknown name fails `tsc`.
 *
 * Elements: `fill: 'currentColor'` fills with the icon colour; `stroke: 'none'`
 * drops the stroke. Everything else strokes with the icon colour.
 */
import {mediaIcons} from './media-icons.ts';

export type IconElement = Readonly<
  | {type: 'path'; d: string; fill?: 'currentColor'; stroke?: 'none'}
  | {type: 'circle'; cx: number; cy: number; r: number; fill?: 'currentColor'; stroke?: 'none'}
  | {type: 'line'; x1: number; y1: number; x2: number; y2: number}
  | {type: 'rect'; x: number; y: number; width: number; height: number; rx?: number; fill?: 'currentColor'; stroke?: 'none'}
  | {type: 'polygon'; points: string; fill?: 'currentColor'; stroke?: 'none'}
  | {type: 'ellipse'; cx: number; cy: number; rx: number; ry: number}
>;

type Entry = Readonly<{meaning: string; elements: readonly IconElement[]; strokeWidth?: number}>;

const glyphs = {
  account: {meaning: "Account", elements: [{type: 'circle', cx: 12, cy: 8, r: 4}, {type: 'path', d: "M4 21a8 8 0 0 1 16 0"}]},
  activity: {meaning: "Activity", elements: [{type: 'path', d: "M3 12h4l3-8 4 16 3-8h4"}]},
  airplay: {meaning: "AirPlay only", elements: [{type: 'path', d: "M5 17H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2h-1"}, {type: 'path', d: "M12 15l5 6H7z"}]},
  archive: {meaning: "Archive", elements: [{type: 'rect', x: 2, y: 3, width: 20, height: 5, rx: 1}, {type: 'path', d: "M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8"}, {type: 'path', d: "M10 12h4"}]},
  arrowLeft: {meaning: "Move left", elements: [{type: 'path', d: "M19 12H5m0 0 6-6m-6 6 6 6"}]},
  arrowRight: {meaning: "Move right", elements: [{type: 'path', d: "M5 12h14m0 0-6-6m6 6-6 6"}]},
  audio: {meaning: "Audio / volume", elements: [{type: 'path', d: "M4 10v4h3l4 4V6L7 10z"}, {type: 'path', d: "M15 9a4 4 0 0 1 0 6M17.5 6.5a7.5 7.5 0 0 1 0 11"}]},
  back: {meaning: "Navigate back / previous", elements: [{type: 'path', d: "M15 5l-7 7 7 7"}]},
  back10: {meaning: "Seek back (the seconds are drawn beside it, never implied)", elements: [{type: 'path', d: "M4 12a8 8 0 1 0 2.3-5.7"}, {type: 'path', d: "M4 4v5h5"}, {type: 'path', d: "M10 15v-5l-1.5 1M14 10h2.5l-2 5"}]},
  bell: {meaning: "Notifications", elements: [{type: 'path', d: "M6 16V11a6 6 0 0 1 12 0v5l2 2H4zM10 21h4"}]},
  bookmark: {meaning: "Watchlist (off)", elements: [{type: 'path', d: "M6 3h12a1 1 0 0 1 1 1v17l-7-4.5L5 21V4a1 1 0 0 1 1-1z"}]},
  bookmarkFilled: {meaning: "Watchlist (on)", elements: [{type: 'path', d: "M6 3h12a1 1 0 0 1 1 1v17l-7-4.5L5 21V4a1 1 0 0 1 1-1z", fill: "currentColor"}]},
  browser: {meaning: "A web browser", elements: [{type: 'rect', x: 2, y: 4, width: 20, height: 16, rx: 2}, {type: 'path', d: "M2 9h20M6 6.5h.01M9 6.5h.01"}]},
  calendar: {meaning: "Date or schedule", elements: [{type: 'rect', x: 3, y: 5, width: 18, height: 16, rx: 2}, {type: 'path', d: "M3 10h18M8 3v4M16 3v4"}]},
  camera: {meaning: "Take or choose a picture", elements: [{type: 'path', d: "M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3z"}, {type: 'circle', cx: 12, cy: 13, r: 3.5}]},
  cast: {meaning: "Google Cast (alias of googleCast; only for Google Cast)", elements: [{type: 'path', d: "M2 8V6a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-6"}, {type: 'path', d: "M2 12a8 8 0 0 1 8 8M2 16a4 4 0 0 1 4 4M2 20h.01"}]},
  category: {meaning: "Category / genre", elements: [{type: 'path', d: "M4 5h6l2 2h8v12H4z"}]},
  channels: {meaning: "Channels / Library Channels", elements: [{type: 'rect', x: 3, y: 6, width: 18, height: 12, rx: 2}, {type: 'path', d: "M3 10h18M8 6v12"}]},
  chapters: {meaning: "Chapters", elements: [{type: 'path', d: "M4 5h16M4 10h16M4 15h10M4 20h6"}]},
  check: {meaning: "Selected, or confirmed (never Watched or Mark read)", elements: [{type: 'path', d: "M4 12.5l5 5L20 7"}]},
  chevronDown: {meaning: "Expands, or opens a menu below", elements: [{type: 'path', d: "M5 9l7 7 7-7"}]},
  chevronUp: {meaning: "Collapses", elements: [{type: 'path', d: "M5 15l7-7 7 7"}]},
  chevronUpDown: {meaning: "Opens a value picker", elements: [{type: 'path', d: "M7 15l5 5 5-5M7 9l5-5 5 5"}]},
  clock: {meaning: "Time, or last checked", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M12 7v5l3 2"}]},
  close: {meaning: "Dismiss an overlay or remove a filter chip (never Archive or Deny)", elements: [{type: 'path', d: "M6 6l12 12M18 6 6 18"}]},
  collection: {meaning: "Collection", elements: [{type: 'rect', x: 3, y: 7, width: 14, height: 14, rx: 2}, {type: 'path', d: "M7 7V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2h-2"}]},
  console: {meaning: "Server console destination", elements: [{type: 'rect', x: 3, y: 4, width: 18, height: 16, rx: 2}, {type: 'path', d: "M7 9l3 3-3 3M12 15h5"}]},
  copy: {meaning: "Copy", elements: [{type: 'rect', x: 9, y: 9, width: 12, height: 12, rx: 2}, {type: 'path', d: "M5 15V5a2 2 0 0 1 2-2h10"}]},
  download: {meaning: "Download", elements: [{type: 'path', d: "M12 4v11m0 0-4-4m4 4 4-4M5 20h14"}]},
  dvr: {meaning: "Recording (DVR)", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'circle', cx: 12, cy: 12, r: 3.5, fill: "currentColor", stroke: "none"}]},
  edit: {meaning: "Edit", elements: [{type: 'path', d: "M4 20h4l11-11-4-4L4 16zM13 7l4 4"}]},
  error: {meaning: "Error", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M9 9l6 6M15 9l-6 6"}]},
  exitFullscreen: {meaning: "Exit full screen", elements: [{type: 'path', d: "M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5"}]},
  external: {meaning: "Opens outside Portico", elements: [{type: 'path', d: "M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5"}]},
  eye: {meaning: "Show / visible", elements: [{type: 'path', d: "M2 12s3.5-6 10-6 10 6 10 6-3.5 6-10 6S2 12 2 12z"}, {type: 'circle', cx: 12, cy: 12, r: 3}]},
  eyeOff: {meaning: "Hide / hidden; Mark as unwatched", elements: [{type: 'path', d: "M3 3l18 18M10.6 5.3A11 11 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4.2M6.4 6.4C3.6 8.4 2 12 2 12s3.5 7 10 7a9.5 9.5 0 0 0 4.2-1"}, {type: 'path', d: "M9.9 9.9a3 3 0 0 0 4.2 4.2"}]},
  filter: {meaning: "Filters", elements: [{type: 'path', d: "M3 5h18l-7 8v6l-4 2v-8z"}]},
  flag: {meaning: "Report a problem", elements: [{type: 'path', d: "M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"}, {type: 'path', d: "M4 22v-7"}]},
  folder: {meaning: "A folder on disk", elements: [{type: 'path', d: "M3 6a1 1 0 0 1 1-1h5l2 2h9a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"}]},
  forward: {meaning: "Navigate forward: pushes a page (row chevron)", elements: [{type: 'path', d: "M9 5l7 7-7 7"}]},
  forward10: {meaning: "Seek forward (seconds drawn beside it)", elements: [{type: 'path', d: "M20 12a8 8 0 1 1-2.3-5.7"}, {type: 'path', d: "M20 4v5h-5"}, {type: 'path', d: "M10 15v-5l-1.5 1M14 10h2.5l-2 5"}]},
  fullscreen: {meaning: "Enter full screen", elements: [{type: 'path', d: "M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"}]},
  globe: {meaning: "Network address / internet", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"}]},
  googleCast: {meaning: "Google Cast only", elements: [{type: 'path', d: "M2 8V6a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-6"}, {type: 'path', d: "M2 12a8 8 0 0 1 8 8M2 16a4 4 0 0 1 4 4M2 20h.01"}]},
  grip: {meaning: "Drag handle: press and drag to move the row", elements: [{type: 'circle', cx: 9, cy: 5, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 15, cy: 5, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 9, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 15, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 9, cy: 19, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 15, cy: 19, r: 1.6, fill: "currentColor", stroke: "none"}]},
  grid: {meaning: "Grid layout", elements: [{type: 'rect', x: 3, y: 3, width: 7, height: 7, rx: 1.5}, {type: 'rect', x: 14, y: 3, width: 7, height: 7, rx: 1.5}, {type: 'rect', x: 3, y: 14, width: 7, height: 7, rx: 1.5}, {type: 'rect', x: 14, y: 14, width: 7, height: 7, rx: 1.5}]},
  heart: {meaning: "Favorite (off)", elements: [{type: 'path', d: "M12 20.5s-8-4.9-8-11A4.5 4.5 0 0 1 12 7a4.5 4.5 0 0 1 8 2.5c0 6.1-8 11-8 11z"}]},
  heartFilled: {meaning: "Favorite (on)", elements: [{type: 'path', d: "M12 20.5s-8-4.9-8-11A4.5 4.5 0 0 1 12 7a4.5 4.5 0 0 1 8 2.5c0 6.1-8 11-8 11z", fill: "currentColor"}]},
  home: {meaning: "Home destination", elements: [{type: 'path', d: "M3 11.5 12 4l9 7.5V20a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z"}]},
  image: {meaning: "A picture / artwork", elements: [{type: 'rect', x: 3, y: 3, width: 18, height: 18, rx: 2}, {type: 'circle', cx: 9, cy: 9, r: 2}, {type: 'path', d: "M21 15l-3.1-3.1a2 2 0 0 0-2.8 0L6 21"}]},
  info: {meaning: "Details / information", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M12 11v6M12 7.5v.5"}]},
  jobs: {meaning: "Scheduled tasks", elements: [{type: 'rect', x: 3, y: 4, width: 18, height: 16, rx: 2}, {type: 'path', d: "M3 9h18M8 4v5M16 4v5M7 14h4M7 17h7"}]},
  keyboard: {meaning: "Keyboard shortcuts / text entry", elements: [{type: 'rect', x: 2, y: 6, width: 20, height: 12, rx: 2}, {type: 'path', d: "M6 10h.01M10 10h.01M14 10h.01M18 10h.01M8 14h8"}]},
  keypad: {meaning: "Enter a code", elements: [{type: 'rect', x: 2, y: 6, width: 20, height: 12, rx: 2}, {type: 'path', d: "M7 12h.01M12 12h.01M17 12h.01"}]},
  library: {meaning: "Libraries", elements: [{type: 'path', d: "M4 4h4v16H4zM10 4h4v16h-4zM16.5 5l4 .8-3.3 14.7-4-.8z"}]},
  link: {meaning: "Link", elements: [{type: 'path', d: "M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"}]},
  list: {meaning: "List layout", elements: [{type: 'path', d: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"}]},
  listPlus: {meaning: "Add to playlist", elements: [{type: 'path', d: "M11 12H3M16 6H3M16 18H3M18 9v6M21 12h-6"}]},
  live: {meaning: "Live TV broadcast (never a device or a TV)", elements: [{type: 'circle', cx: 12, cy: 12, r: 2.5}, {type: 'path', d: "M7.5 7.5a6.5 6.5 0 0 0 0 9M16.5 7.5a6.5 6.5 0 0 1 0 9M4.5 4.5a10.5 10.5 0 0 0 0 15M19.5 4.5a10.5 10.5 0 0 1 0 15"}]},
  lock: {meaning: "Locked, PIN, or always-on (required)", elements: [{type: 'rect', x: 5, y: 11, width: 14, height: 10, rx: 2}, {type: 'path', d: "M8 11V8a4 4 0 0 1 8 0v3"}]},
  logs: {meaning: "Logs and diagnostics", elements: [{type: 'path', d: "M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z"}, {type: 'path', d: "M14 2v5h6"}, {type: 'path', d: "M8 13h8M8 17h8M8 9h2"}]},
  lyrics: {meaning: "Lyrics", elements: [{type: 'path', d: "M9 18V5l11-2v13M9 8l11-2M6 20a3 3 0 1 0 0-.01M17 18a3 3 0 1 0 0-.01"}]},
  mail: {meaning: "Message or invitation", elements: [{type: 'rect', x: 3, y: 5, width: 18, height: 14, rx: 2}, {type: 'path', d: "M3 8l9 6 9-6"}]},
  markUnread: {meaning: "Mark as unread", elements: [{type: 'path', d: "M21 11V6a1 1 0 0 0-1-1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h9"}, {type: 'path', d: "M3 7l9 6 9-6"}, {type: 'circle', cx: 19, cy: 17.5, r: 2.5, fill: "currentColor", stroke: "none"}]},
  maximize: {meaning: "Open or expand (e.g. open the player)", elements: [{type: 'path', d: "M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7"}]},
  minus: {meaning: "Decrease", elements: [{type: 'path', d: "M5 12h14"}]},
  more: {meaning: "More actions (horizontal)", elements: [{type: 'circle', cx: 5, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 12, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 19, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}]},
  moreVertical: {meaning: "More actions (vertical)", elements: [{type: 'circle', cx: 12, cy: 5, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 12, cy: 12, r: 1.6, fill: "currentColor", stroke: "none"}, {type: 'circle', cx: 12, cy: 19, r: 1.6, fill: "currentColor", stroke: "none"}]},
  mute: {meaning: "Muted", elements: [{type: 'path', d: "M4 10v4h3l4 4V6L7 10z"}, {type: 'path', d: "M16 9l5 6M21 9l-5 6"}]},
  network: {meaning: "Network settings", elements: [{type: 'circle', cx: 12, cy: 5, r: 2.5}, {type: 'circle', cx: 5, cy: 19, r: 2.5}, {type: 'circle', cx: 19, cy: 19, r: 2.5}, {type: 'path', d: "M12 7.5v5m0 0-5.5 4.5M12 12.5l5.5 4.5"}]},
  next: {meaning: "Next item / Play next", elements: [{type: 'path', d: "M5 5v14l11-7z", fill: "currentColor", stroke: "none"}, {type: 'line', x1: 19, y1: 5, x2: 19, y2: 19}]},
  pause: {meaning: "Pause", elements: [{type: 'rect', x: 6, y: 4, width: 4, height: 16, rx: 1, fill: "currentColor", stroke: "none"}, {type: 'rect', x: 14, y: 4, width: 4, height: 16, rx: 1, fill: "currentColor", stroke: "none"}]},
  people: {meaning: "Several people / Watch Together", elements: [{type: 'circle', cx: 9, cy: 8, r: 3.5}, {type: 'path', d: "M2.5 20a6.5 6.5 0 0 1 13 0M16 4.5a3.5 3.5 0 0 1 0 7M21.5 20a6.5 6.5 0 0 0-4.5-6.2"}]},
  person: {meaning: "A person (cast, crew, member)", elements: [{type: 'circle', cx: 12, cy: 8, r: 4}, {type: 'path', d: "M4 21a8 8 0 0 1 16 0"}]},
  phone: {meaning: "A phone", elements: [{type: 'rect', x: 6, y: 2, width: 12, height: 20, rx: 2}, {type: 'path', d: "M11 18h2"}]},
  pip: {meaning: "Picture in Picture", elements: [{type: 'rect', x: 2, y: 4, width: 20, height: 16, rx: 2}, {type: 'rect', x: 12, y: 11, width: 8, height: 6, rx: 1, fill: "currentColor", stroke: "none"}]},
  play: {meaning: "Play", elements: [{type: 'polygon', points: "7,4 20,12 7,20", fill: "currentColor", stroke: "none"}]},
  plus: {meaning: "Create or add a new thing (never add-to-playlist)", elements: [{type: 'path', d: "M12 5v14M5 12h14"}]},
  previous: {meaning: "Previous item", elements: [{type: 'path', d: "M19 5v14L8 12z", fill: "currentColor", stroke: "none"}, {type: 'line', x1: 5, y1: 5, x2: 5, y2: 19}]},
  profile: {meaning: "A profile (a person, never a device)", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'circle', cx: 12, cy: 10, r: 3}, {type: 'path', d: "M6.5 18.5a6 6 0 0 1 11 0"}]},
  pulse: {meaning: "Server health / overview", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M7 12h3l2-4 2 8 2-4h1"}]},
  qr: {meaning: "Scan a QR code", elements: [{type: 'rect', x: 3, y: 3, width: 7, height: 7, rx: 1}, {type: 'rect', x: 14, y: 3, width: 7, height: 7, rx: 1}, {type: 'rect', x: 3, y: 14, width: 7, height: 7, rx: 1}, {type: 'path', d: "M14 14h3v3M21 14v3h-3M14 21h3M21 21h.01"}]},
  quality: {meaning: "Quality", elements: [{type: 'rect', x: 3, y: 5, width: 18, height: 14, rx: 2}, {type: 'path', d: "M7 15V9l3 3 3-3v6M16 9h1.5a1.5 1.5 0 0 1 0 3H16zM16 12l2.5 3"}]},
  queue: {meaning: "Play queue / Add to queue (never playlists)", elements: [{type: 'path', d: "M4 6h12M4 12h12M4 18h8M18 12l3 2-3 2z"}]},
  refresh: {meaning: "Reload data (never Play from beginning)", elements: [{type: 'path', d: "M20 12a8 8 0 1 1-2.3-5.7M20 4v5h-5"}]},
  repeat: {meaning: "Repeat, or a repeating rule", elements: [{type: 'path', d: "M17 2l4 4-4 4M3 11V9a3 3 0 0 1 3-3h15M7 22l-4-4 4-4M21 13v2a3 3 0 0 1-3 3H3"}]},
  restart: {meaning: "Play from the beginning", elements: [{type: 'path', d: "M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"}, {type: 'path', d: "M3 3v5h5"}, {type: 'polygon', points: "10.5,9 15.5,12 10.5,15", fill: "currentColor", stroke: "none"}]},
  saved: {meaning: "Saved destination (Watchlist, Favorites, playlists)", elements: [{type: 'path', d: "M6 3h12a1 1 0 0 1 1 1v17l-7-4.5L5 21V4a1 1 0 0 1 1-1z"}]},
  search: {meaning: "Search", elements: [{type: 'circle', cx: 11, cy: 11, r: 6.5}, {type: 'line', x1: 16, y1: 16, x2: 21, y2: 21}]},
  server: {meaning: "A server", elements: [{type: 'rect', x: 3, y: 4, width: 18, height: 7, rx: 2}, {type: 'rect', x: 3, y: 13, width: 18, height: 7, rx: 2}, {type: 'path', d: "M7 7.5h.01M7 16.5h.01"}]},
  settings: {meaning: "App or server settings (never Customize Home or playback options)", elements: [{type: 'circle', cx: 12, cy: 12, r: 3}, {type: 'path', d: "M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"}]},
  shield: {meaning: "Security and access (never locks)", elements: [{type: 'path', d: "M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"}]},
  shuffle: {meaning: "Shuffle", elements: [{type: 'path', d: "M3 7h3.5a4 4 0 0 1 3.2 1.6l4.6 6.8A4 4 0 0 0 17.5 17H21m0 0-2.5-2.5M21 17l-2.5 2.5M3 17h3.5a4 4 0 0 0 3.2-1.6l.6-.9M21 7h-3.5a4 4 0 0 0-3.2 1.6l-.6.9M21 7l-2.5-2.5M21 7l-2.5 2.5"}]},
  signOut: {meaning: "Sign out", elements: [{type: 'path', d: "M10 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h5M15 8l4 4-4 4M19 12H9"}]},
  sleep: {meaning: "Sleep timer", elements: [{type: 'path', d: "M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"}]},
  sliders: {meaning: "Customize or adjust (Customize Home, playback options)", elements: [{type: 'path', d: "M21 4h-7M10 4H3M21 12h-9M8 12H3M21 20h-5M12 20H3M14 2v4M8 10v4M16 18v4"}]},
  sort: {meaning: "Sort", elements: [{type: 'path', d: "M4 7h10M4 12h7M4 17h4M17 6v12m0 0-3-3m3 3 3-3"}]},
  speed: {meaning: "Playback speed", elements: [{type: 'path', d: "M4 16a8 8 0 1 1 16 0"}, {type: 'path', d: "M12 16l4-5"}, {type: 'circle', cx: 12, cy: 16, r: 1.5, fill: "currentColor", stroke: "none"}]},
  star: {meaning: "Rate (off)", elements: [{type: 'polygon', points: "12,3 14.8,9 21,9.6 16.3,13.9 17.7,20 12,16.8 6.3,20 7.7,13.9 3,9.6 9.2,9"}]},
  starFilled: {meaning: "Rated", elements: [{type: 'polygon', points: "12,3 14.8,9 21,9.6 16.3,13.9 17.7,20 12,16.8 6.3,20 7.7,13.9 3,9.6 9.2,9", fill: "currentColor"}]},
  stop: {meaning: "Stop, or cancel a running job", elements: [{type: 'rect', x: 5, y: 5, width: 14, height: 14, rx: 2, fill: "currentColor", stroke: "none"}]},
  storage: {meaning: "Storage", elements: [{type: 'ellipse', cx: 12, cy: 6, rx: 8, ry: 3}, {type: 'path', d: "M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"}]},
  subtitles: {meaning: "Subtitles", elements: [{type: 'rect', x: 3, y: 5, width: 18, height: 14, rx: 2}, {type: 'path', d: "M6.5 12h5M14 12h3.5M6.5 15.5h2M11 15.5h6.5"}]},
  success: {meaning: "Success", elements: [{type: 'circle', cx: 12, cy: 12, r: 9}, {type: 'path', d: "M8 12.5l3 3 5.5-6"}]},
  switch: {meaning: "Switch profile or server", elements: [{type: 'path', d: "M4 7h13m0 0-3-3m3 3-3 3M20 17H7m0 0 3-3m-3 3 3 3"}]},
  tablet: {meaning: "A tablet", elements: [{type: 'rect', x: 4, y: 2, width: 16, height: 20, rx: 2}, {type: 'path', d: "M11 18h2"}]},
  thumbDown: {meaning: "Dislike", elements: [{type: 'path', d: "M17 13V4h3a1 1 0 0 1 1 1v7a1 1 0 0 1-1 1zm0 0-4 8a2.5 2.5 0 0 1-2.5-2.5V15H5a2 2 0 0 1-2-2.3l1.2-7A2 2 0 0 1 6.2 4H17"}]},
  thumbUp: {meaning: "Like", elements: [{type: 'path', d: "M7 11v9H4a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1zm0 0 4-8a2.5 2.5 0 0 1 2.5 2.5V9H19a2 2 0 0 1 2 2.3l-1.2 7a2 2 0 0 1-2 1.7H7"}]},
  transcode: {meaning: "Transcoding", elements: [{type: 'path', d: "M4 7h11l-3-3M20 17H9l3 3M4 7v4M20 17v-4"}]},
  trash: {meaning: "Delete or remove", elements: [{type: 'path', d: "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6"}]},
  tvDevice: {meaning: "A TV device (Apple TV, Android TV, a Portico TV)", elements: [{type: 'rect', x: 2, y: 7, width: 20, height: 14, rx: 2}, {type: 'path', d: "M17 2l-5 5-5-5"}]},
  unlock: {meaning: "Unlocked", elements: [{type: 'rect', x: 5, y: 11, width: 14, height: 10, rx: 2}, {type: 'path', d: "M8 11V8a4 4 0 0 1 8 0"}]},
  upload: {meaning: "Upload a file", elements: [{type: 'path', d: "M12 15V4m0 0-4 4m4-4 4 4M5 20h14"}]},
  warning: {meaning: "Warning (never an action such as Report a problem)", elements: [{type: 'path', d: "M12 3 2.5 20h19z"}, {type: 'path', d: "M12 9v5M12 17v.5"}]},
  watched: {meaning: "Watched state / Mark as watched", elements: [{type: 'path', d: "M1.5 12s3.8-6.5 10.5-6.5c4.2 0 7.2 2.6 8.9 4.6"}, {type: 'path', d: "M11 18.4C5.2 17.9 1.5 12 1.5 12"}, {type: 'circle', cx: 12, cy: 12, r: 2.75}, {type: 'path', d: "M14.5 17.5l2.25 2.25L21.5 15"}]},
  wrench: {meaning: "Maintenance", elements: [{type: 'path', d: "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94z"}]},
} as const satisfies Record<string, Entry>;

const family = (name: keyof typeof mediaIcons, meaning: string): Entry => ({meaning, strokeWidth: mediaIcons[name].strokeWidth, elements: mediaIcons[name].primitives as readonly IconElement[]});

const families = {
  film: family('film', 'Movie library family'),
  tv: family('tv', 'TV library family (a library, never a device)'),
  book: family('book', 'Audiobook library family'),
  music: family('music', 'Music library family'),
} as const satisfies Record<string, Entry>;

export type IconId = keyof typeof glyphs | keyof typeof families;
export type IconDefinition = Readonly<{id: IconId; meaning: string; strokeWidth: number; elements: readonly IconElement[]}>;

/** Default stroke width for every glyph that doesn't set its own. */
export const iconStrokeWidth = 1.75;

export const icons: Readonly<Record<IconId, IconDefinition>> = Object.freeze(
  Object.fromEntries(
    Object.entries({...glyphs, ...families} as Record<IconId, Entry>).map(([id, entry]) => [id, Object.freeze({id: id as IconId, meaning: entry.meaning, strokeWidth: entry.strokeWidth ?? iconStrokeWidth, elements: entry.elements})]),
  ) as Record<IconId, IconDefinition>,
);

export const iconIds: readonly IconId[] = Object.freeze(Object.keys(icons) as IconId[]);

/** Narrow a string (e.g. from server data) to a known icon; unknown names are not rendered. */
export function isIconId(value: string): value is IconId {
  return Object.prototype.hasOwnProperty.call(icons, value);
}
