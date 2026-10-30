/**
 * System surfaces: what the operating system draws for Portico (Siri, Spotlight,
 * Now Playing, Top Shelf, Android TV and notification channels, the Local Network
 * prompt). Rules: design b4cf92e, notes/system-copy.md. Only plain `{name}`
 * arguments: native code fills them without ICU. Siri phrases carry `{app}`
 * exactly once; Apple puts the app's name there.
 */
export const systemMessages = {
  'system.siri.resume.title': 'Resume watching',
  'system.siri.resume.description': 'Picks up where you left off.',
  'system.siri.play.title': 'Play a title',
  'system.siri.play.description': 'Plays something from Continue Watching or My List.',
  'system.siri.play.ask': 'Which title?',
  'system.siri.title': 'Title',
  'system.siri.phrase.resume': 'Resume {app}',
  'system.siri.phrase.continue': 'Continue watching in {app}',
  'system.siri.phrase.play': 'Play {title} in {app}',
  'system.siri.phrase.watch': 'Watch {title} in {app}',
  'system.spotlight.continueWatching': 'Continue Watching',
  'system.spotlight.myList': 'My List',
  'system.nowPlaying.episodeLine': '{label}, {title}',
  'system.apple.localNetwork.ios': 'Portico looks for servers, TVs and Google Cast devices on your network. Without it, addresses and TV codes still work.',
  'system.apple.localNetwork.tvos': 'Portico looks for servers and TVs on your network. Without it, addresses and sign-in codes still work.',
  'system.android.channel.downloads': 'Downloads',
  'system.android.channel.playback': 'Now playing',
  'system.android.download.running': 'Downloading',
} as const;
