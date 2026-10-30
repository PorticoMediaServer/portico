/** Reference-only values. Imported directly by the serve-only shell family. */
export const shellFixtures = {
  'next.dev.shell.serverName': 'Portico demo',
  'next.dev.shell.uptime': 'Up for 6 days',
  'next.dev.shell.storage': '1.2 TB free',
  'next.dev.shell.database': 'Database 412 MB',
} as const;

export const shellReference = {
  current: {id: 'demo', username: 'demo', name: 'Demo', initial: 'D', art: 'blue'},
  profiles: [{id: 'alex', name: 'Alex', initial: 'A', art: 'violet', pinRequired: true}, {id: 'kids', name: 'Kids', initial: 'K', art: 'mint'}],
  accountFacts: [{id: 'server', text: 'Portico demo', role: 'context'}, {id: 'kind', text: 'Server account', role: 'context'}],
  accountDescription: 'Signed in to Portico demo as demo',
  room: 'Movie night',
  participants: [{id: 'demo', initial: 'D', colour: '#2f86bb'}, {id: 'alex', initial: 'A', colour: '#685294'}, {id: 'sam', initial: 'S', colour: '#985243'}],
  waitingFor: 'Sam',
  lastScan: 'today at 3:12 PM',
  latest: '0.2.0', currentVersion: '0.1.0',
  streams: [
    {id: 'sam', username: 'sam', startedAt: '2026-10-22T12:00:00Z', showId: 'tlou', name: 'Sam', initial: 'S', art: 'mint', device: 'Living room TV', title: 'The Last of Us', episodeTitle: 'Long, Long Time', season: 1, episode: 3, positionMs: 420, durationMs: 1000},
    {id: 'priya', username: 'priya', startedAt: '2026-10-22T11:00:00Z', name: 'Priya', initial: 'P', art: 'violet', device: 'iPhone', title: 'Spirited Away', year: 2001, positionMs: 430, durationMs: 1000},
  ],
  activityCounts: {overflowStreams: 24, overflowTasks: 6},
  activityProblem: 'Buffering',
  longUsername: 'a-username-with-a-long-identity', longProfile: 'A different family profile with a long name', longDevice: 'The living room television with a long device name',
  conversion: 'Converting to 1080p',
  scan: {id: 'movies', title: 'Movies', current: 'Practical Magic 2 (2026).mkv', processed: 51, total: 80, discovered: 80},
  tasks: [{id: 'previews', title: 'Seek-bar pictures', detail: 'TV Shows', progressText: '38 of 96 episodes', processed: 38, total: 96, icon: 'i-image'}, {id: 'intros', title: 'Finding intros and credits', detail: 'Anime', progressText: '9 of 32 episodes', processed: 9, total: 32, icon: 'i-skip'}],
  notifications: [
    {id: 'download', title: 'Download ready', body: 'Spirited Away finished downloading and is ready to play offline.', when: '20 min ago', action: 'Open', asset: 'pool/movies/spirited-away.jpg'},
    {id: 'report', title: 'Your report has an update', body: 'The report you sent is now marked resolved.', when: 'Yesterday', about: 'About “Rick and Morty”', action: 'Open report', asset: 'pool/tv/rick-and-morty.jpg'},
    {id: 'device', title: 'New device signed in', body: 'Living room TV was approved for this account. If this was not you, remove it and change your password.', when: 'Monday', action: 'Review security', secondAction: 'Review device'},
  ],
  phoneNotifications: [
    {id: 'download', title: 'Download ready', body: 'Work Drinks finished downloading and is ready to play offline.', when: '12 min ago', action: 'Open', icon: 'i-dl'},
    {id: 'recording', title: 'Recording started late', body: 'Harbor City This Morning started late because every tuner was being used to watch live TV. The start of the programme is missing.', when: 'Today, 3:00 PM', action: 'Review recordings', icon: 'i-rec', warning: true},
    {id: 'feedback', title: 'New viewer feedback', body: 'A viewer reported a problem and it is waiting for review.', when: 'Yesterday', action: 'Open report', icon: 'i-lyrics'},
  ],
  editor: {assets: ['pool/movies/toy-story.jpg', 'pool/movies/toy-story-2.jpg', 'pool/movies/toy-story-3.jpg', 'pool/movies/toy-story-5.jpg'], targets: 'Toy Story, Toy Story 2, Toy Story 3, Toy Story 5', title: 'Edit 4 movies', description: 'Changes apply to all four. A field that differs shows Mixed and is left alone unless you change it.', phoneDescription: 'Changes apply to all four. Mixed fields are left alone unless you change them.'},
  accountNav: [
    {id: 'profile', label: 'Profile & account', icon: 'i-user'}, {id: 'playback', label: 'Playback', icon: 'i-playc'},
    {id: 'languages', label: 'Audio & subtitles', icon: 'i-cc'}, {id: 'quality', label: 'Quality & data', icon: 'i-gauge'},
    {id: 'music', label: 'Music', icon: 'i-music'}, {id: 'appearance', label: 'Appearance', icon: 'i-sliders'},
    {id: 'region', label: 'Language & region', icon: 'i-globe'}, {id: 'privacy', label: 'Privacy', icon: 'i-shield'},
    {id: 'device', label: 'This device', icon: 'i-monitor'}, {id: 'about', label: 'About Portico', icon: 'i-info'},
  ],
  serverNav: [
    {id: 'dashboard', label: 'Dashboard', icon: 'i-pulse', cluster: 'overview', count: 2, warning: true}, {id: 'history', label: 'Play history', icon: 'i-clock', cluster: 'overview'},
    {id: 'libraries', label: 'Libraries', icon: 'i-library', cluster: 'media', count: 3, warning: true}, {id: 'metadata', label: 'Metadata', icon: 'i-image', cluster: 'media'}, {id: 'segments', label: 'Intros and credits', icon: 'i-skip', cluster: 'media', count: 7}, {id: 'live', label: 'Live TV & Channels', icon: 'i-tv', cluster: 'media'},
    {id: 'people', label: 'People', icon: 'i-people', cluster: 'access', count: 1, warning: true}, {id: 'remote', label: 'Remote access', icon: 'i-wifi', cluster: 'access'}, {id: 'integrations', label: 'Integrations', icon: 'i-link', cluster: 'access'},
    {id: 'streaming', label: 'Streaming', icon: 'i-swap', cluster: 'system'}, {id: 'storage', label: 'Storage & backups', icon: 'i-storage', cluster: 'system'}, {id: 'schedule', label: 'Schedule', icon: 'i-cal', cluster: 'system'}, {id: 'troubleshooting', label: 'Troubleshooting', icon: 'i-wrench', cluster: 'system'}, {id: 'general', label: 'General', icon: 'i-server', cluster: 'system'},
  ],
  phoneAccountNav: [{id: 'playback', label: 'Playback', icon: 'i-play'}, {id: 'languages', label: 'Audio & subtitles', icon: 'i-cc'}, {id: 'quality', label: 'Quality & data', icon: 'i-gauge'}, {id: 'music', label: 'Music', icon: 'i-note'}, {id: 'appearance', label: 'Appearance', icon: 'i-filter'}, {id: 'region', label: 'Language & region', icon: 'i-globe'}, {id: 'privacy', label: 'Privacy', icon: 'i-shield'}, {id: 'device', label: 'This device', icon: 'i-phone'}],
  phoneServerNav: [{id: 'dashboard', label: 'Dashboard', icon: 'i-pulse', trailing: '2 playing'}, {id: 'invite', label: 'Invite someone', icon: 'i-userplus'}, {id: 'history', label: 'Play history', icon: 'i-clock'}, {id: 'libraries', label: 'Libraries', icon: 'i-grid', trailing: '5'}, {id: 'metadata', label: 'Metadata', icon: 'i-image'}, {id: 'segments', label: 'Intros and credits', icon: 'i-next'}, {id: 'live', label: 'Live TV & Channels', icon: 'i-tv', trailing: '14 channels'}, {id: 'people', label: 'People', icon: 'i-people'}, {id: 'integrations', label: 'Integrations', icon: 'i-plug'}, {id: 'streaming', label: 'Streaming', icon: 'i-film'}, {id: 'remote', label: 'Remote access', icon: 'i-link', trailing: 'On'}, {id: 'storage', label: 'Storage & backups', icon: 'i-disk'}, {id: 'schedule', label: 'Schedule', icon: 'i-cal'}, {id: 'troubleshooting', label: 'Troubleshooting', icon: 'i-wrench'}, {id: 'general', label: 'General', icon: 'i-cog'}],
  rows: {autoplay: 'Play the next episode automatically', autoplayHelp: 'Start the next episode when one ends.', wait: 'Wait before the next episode', waitHelp: 'A countdown before it starts, so there’s time to stop it.', credits: 'Start the next episode during the credits', creditsHelp: 'When the credits are clearly marked, count down and start the next episode without waiting for them to end. Off: the next episode starts when this one ends.'},
  seconds: [{value: '0', label: 'Immediately'}, {value: '5', label: '5 seconds'}, {value: '10', label: '10 seconds'}, {value: '15', label: '15 seconds'}],
  settingsQuery: 'subtitle',
  settingsSearch: [
    {id: 'languages', label: 'Audio & subtitles', icon: 'i-cc', area: 'account', rows: [{id: 'show-subtitles', label: 'Show subtitles'}, {id: 'subtitle-languages', label: 'Preferred subtitle languages'}, {id: 'subtitle-size', label: 'Size', description: 'In Subtitle appearance'}, {id: 'subtitle-background', label: 'Background', description: 'In Subtitle appearance'}]},
    {id: 'storage', label: 'Storage & backups', icon: 'i-storage', area: 'server', rows: [{id: 'subtitle-tracks', label: 'Subtitle tracks per file'}, {id: 'subtitle-file', label: 'Largest subtitle file'}, {id: 'subtitle-retention', label: 'Keep fetched subtitles for', description: 'In What Portico keeps'}]},
    {id: 'metadata', label: 'Metadata', icon: 'i-image', area: 'server', rows: [{id: 'opensubtitles-key', label: 'OpenSubtitles API key', description: 'In Subtitles found online'}, {id: 'opensubtitles-user', label: 'OpenSubtitles username'}, {id: 'opensubtitles-password', label: 'OpenSubtitles password'}]},
  ],
  skipping: 'Skipping',
  skipOptions: [{value: 'ask', label: 'Ask each time', description: 'Shows a Skip button'}, {value: 'auto', label: 'Skip automatically', description: 'Without asking'}, {value: 'off', label: 'Never', description: 'Plays it through'}],
  skipRows: [{id: 'intro', label: 'Intros', value: 'ask'}, {id: 'credits', label: 'Credits', value: 'ask'}, {id: 'recaps', label: 'Recaps', value: 'auto'}],
  method: 'Playback method',
  methodOptions: [{value: 'auto', label: 'Automatic (recommended)', description: 'Plays the original file when this browser can, and converts it when it can’t.'}, {value: 'original', label: 'Always play the original', description: 'Never converts. A title this browser can’t play won’t start.'}, {value: 'convert', label: 'Always convert', description: 'The server converts every title, even when the original would play.'}],
  languages: [{id: 'en', label: 'English'}, {id: 'ja', label: 'Japanese'}],
  optionalLanguage: {id: 'fr', label: 'French'},
  metrics: [{id: 'cpu', label: 'Processor', value: '18%', now: 18, peak: 30, seed: 14, wobble: 6}, {id: 'ram', label: 'Memory', value: '41%', now: 41, peak: 44, seed: 38, wobble: 2}, {id: 'sent', label: 'Sent to viewers', value: '14 Mb/s', now: 14, peak: 40, seed: 8, wobble: 5}, {id: 'disk', label: 'Disk reads', value: '21 MB/s', now: 21, peak: 60, seed: 12, wobble: 10}, {id: 'gpu', label: 'Graphics processor', value: '9%', now: 9, peak: 30, seed: 6, wobble: 4}, {id: 'encoder', label: 'Video encoder', value: '12%', now: 12, peak: 30, seed: 3, wobble: 6}],
  statistics: [{id: 'plays', label: 'Plays', value: '46'}, {id: 'people', label: 'People', value: '4'}, {id: 'hours', label: 'Hours watched', value: '31'}, {id: 'converted', label: 'Converted', value: '7'}],
  segmentStatistics: [{id: 'measured', label: 'Files measured', value: '124'}, {id: 'waiting', label: 'Waiting', value: '4'}, {id: 'unreadable', label: 'Couldn’t be read', value: '0'}, {id: 'status', label: 'Status', value: 'Working in the background', status: true}],
  barTitle: 'Most played', bars: [{id: 'tlou', label: 'The Last of Us', value: 12, valueText: '12'}, {id: 'severance', label: 'Severance', value: 9, valueText: '9'}, {id: 'spirited', label: 'Spirited Away', value: 6, valueText: '6'}, {id: 'abbey', label: 'Abbey Road (2019 Mix)', value: 5, valueText: '5'}, {id: 'solo', label: 'Solo Leveling', value: 4, valueText: '4'}],
  personBarTitle: 'Most active', personBars: [{id: 'sam', name: 'Sam', initial: 'S', art: 'mint', value: 18, valueText: '18'}, {id: 'demo', name: 'Demo', initial: 'D', art: 'blue', value: 14, valueText: '14'}, {id: 'priya', name: 'Priya', initial: 'P', art: 'violet', value: 9, valueText: '9'}, {id: 'nana', name: 'Nana', initial: 'N', art: 'gold', value: 5, valueText: '5'}],
  segmentDuration: 2832,
  opening: [{id: 'recap', kind: 'recap', label: 'Recap', start: 0, end: 38, suggested: false, trust: 'Can skip automatically', trustTone: 'automatic', facts: [{id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}, {id: 'intro', kind: 'intro', label: 'Intro', start: 130, end: 201.5, suggested: false, trust: 'Adjusting', trustTone: 'suggested', facts: [{id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}],
  ending: [{id: 'credits', kind: 'credits', label: 'Credits', start: 2720, end: 2832, suggested: false, trust: 'Can skip automatically', trustTone: 'automatic', facts: [{id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}],
  segmentExamples: [
    {duration: 3422, opening: [{id: 'intro', kind: 'intro', label: 'Intro', start: 581, end: 652, suggested: false, trust: 'Approved', trustTone: 'neutral', facts: [{id: 'basis', text: 'Added by you', role: 'supplement'}]}], ending: [{id: 'credits', kind: 'credits', label: 'Credits', start: 3310, end: 3422, suggested: false, trust: 'Can skip automatically', trustTone: 'automatic', facts: [{id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}]},
    {duration: 3200, opening: [{id: 'recap', kind: 'recap', label: 'Recap', start: 0, end: 45, suggested: true, trust: 'Viewers can skip', trustTone: 'suggested', facts: [{id: 'confidence', text: '74% sure', role: 'supplement'}, {id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}, {id: 'intro', kind: 'intro', label: 'Intro', start: 182, end: 253, suggested: true, trust: 'Viewers can skip', trustTone: 'suggested', facts: [{id: 'confidence', text: '91% sure', role: 'supplement'}, {id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}], ending: [{id: 'credits', kind: 'credits', label: 'Credits', start: 3095, end: 3200, suggested: false, trust: 'Can skip automatically', trustTone: 'automatic', facts: [{id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}]},
    {duration: 3278, opening: [{id: 'intro', kind: 'intro', label: 'Intro', start: 276, end: 347, suggested: true, trust: 'Viewers can skip', trustTone: 'suggested', facts: [{id: 'confidence', text: '88% sure', role: 'supplement'}, {id: 'basis', text: 'Matched across episodes', role: 'supplement'}]}], ending: [{id: 'credits', kind: 'credits', label: 'Credits', start: 3166, end: 3278, suggested: true, trust: 'Viewers can skip', trustTone: 'suggested', facts: [{id: 'confidence', text: '69% sure', role: 'supplement'}, {id: 'basis', text: 'From a chapter title', role: 'supplement'}]}]},
  ],
  accountPerson: {id: 'sam', name: 'Sam', initial: 'S', art: 'mint'},
  personFacts: [{id: 'sign-in', text: 'Portico Account', role: 'context'}, {id: 'profiles', text: '1 profile', role: 'context'}, {id: 'libraries', text: 'All libraries', role: 'context'}, {id: 'limit', text: '2 streams at once', role: 'context'}],
  people: [
    {person: {id: 'demo', name: 'Demo', initial: 'D', art: 'blue'}, role: 'owner', self: true, facts: [{id: 'sign-in', text: 'Portico Account', role: 'context'}, {id: 'profiles', text: '3 profiles', role: 'context'}, {id: 'libraries', text: 'All libraries', role: 'context'}], activity: 'Listening now', more: 'More for Demo'},
    {person: {id: 'priya', name: 'Priya', initial: 'P', art: 'violet'}, role: 'administrator', facts: [{id: 'sign-in', text: 'Portico Account', role: 'context'}, {id: 'profiles', text: '2 profiles', role: 'context'}, {id: 'libraries', text: 'Movies, TV Shows and Anime', role: 'context'}], activity: 'Watching now, away', more: 'More for Priya'},
    {person: {id: 'sam', name: 'Sam', initial: 'S', art: 'mint'}, role: 'member', facts: [{id: 'sign-in', text: 'Portico Account', role: 'context'}, {id: 'profiles', text: '1 profile', role: 'context'}, {id: 'libraries', text: 'All libraries', role: 'context'}, {id: 'limit', text: '2 streams at once', role: 'context'}], activity: 'Watching now', more: 'More for Sam'},
    {person: {id: 'nana', name: 'Nana', initial: 'N', art: 'gold'}, role: 'member', facts: [{id: 'sign-in', text: 'An account on this server', role: 'context'}, {id: 'profiles', text: '1 profile', role: 'context'}, {id: 'libraries', text: 'All libraries', role: 'context'}], activity: 'Watching now', more: 'More for Nana'},
    {person: {id: 'marcus', name: 'Marcus', initial: 'M', art: 'slate'}, role: 'member', disabled: true, facts: [{id: 'sign-in', text: 'Portico Account', role: 'context'}, {id: 'profiles', text: '1 profile', role: 'context'}, {id: 'disabled', text: 'Turned off 2 October', role: 'context'}], activity: 'Last active in September', more: 'More for Marcus'},
  ],
  watching: 'Watching now', limits: 'Limits…', moreSam: 'More for Sam',
  device: 'Android TV', deviceFacts: [{id: 'seen', text: 'First seen today at 3:48 PM', role: 'context'}, {id: 'network', text: 'Home network', role: 'context'}],
  approve: 'Approve', block: 'Block', approved: 'Approved', blocked: 'Blocked',
  waitingNotice: '1 device is waiting for your approval.', waitingBody: 'An Android TV signed in as Sam at 3:48 PM. It can’t browse or play until you approve it.', review: 'Review',
  artworkBase: 'http://100.120.50.24:8787/assets/', subtitleAsset: 'pool/movies/dune-part-two--bd.jpg',
} as const;

/** Exact seeded inputs from desktop-settings.html; used only for reference rendering. */
export function shellReferenceSamples(now: number, seed: number, wobble: number): number[] {
  let value = seed;
  return Array.from({length: 30}, (_, index) => { value = (value * 9301 + 49297) % 233280; return now + (value / 233280 - .5) * wobble * .9 + Math.sin(index / 5 + seed) * wobble * .8; });
}

/** Literal values from desktop-activity.html at 5cbcc036; serve-only, never wire data. */
export const activityR7Reference = {
  streams: {
    priya: {id: 'priya', username: 'priya.n', name: 'priya.n', initial: 'P', art: 'violet', device: 'iPhone', title: 'Spirited Away', subtitle: [{id: 'year', text: '2001', role: 'context'}], progress: 43, conversion: 'Converting to 1080p'},
    sam: {id: 'sam', username: 'sam_k', name: 'Sam', initial: 'S', art: 'mint', device: 'Living room TV', title: 'The Last of Us', subtitle: [{id: 'episode-label', text: 'S1 E3', role: 'context'}, {id: 'episode-title', text: 'Long, Long Time', role: 'context'}], progress: 42},
    nana: {id: 'nana', username: 'nana_r', name: 'Nana', initial: 'N', art: 'gold', device: 'Kitchen iPad', title: 'World Tonight', subtitle: [{id: 'channel', text: 'Northwind News', role: 'context'}], progress: 50},
    alex: {id: 'alex', username: 'alex_m', name: 'Alex', initial: 'A', art: 'rose', device: 'Pixel 8', title: 'Barbie', subtitle: [{id: 'year', text: '2023', role: 'context'}], progress: 61, problem: 'Buffering'},
    jordan: {id: 'jordan', username: 'jordan', name: 'Kids', initial: 'K', art: 'sky', device: 'Bedroom TV', title: 'Inside Out', subtitle: [{id: 'year', text: '2015', role: 'context'}], progress: 34, conversion: 'Converting to 720p'},
    dana: {id: 'dana', username: 'dana', name: 'dana', initial: 'D', art: 'slate', device: 'Fire TV', title: 'Reacher', subtitle: [{id: 'episode-label', text: 'S2 E5', role: 'context'}], progress: 12, conversion: 'Converting to 1080p'},
  },
} as const;

/** Drawn Activity frame 7 only; not a production status or API response. */
export const activityR9Reference = {
  title: 'Archive can’t be reached', description: 'Remote source', since: 'since 2:14 PM',
  destination: 'Storage & backups', resolve: 'Resolve issue',
} as const;
