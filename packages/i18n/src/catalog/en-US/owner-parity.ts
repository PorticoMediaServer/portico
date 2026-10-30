/**
 * en-US copy for owner tools that reached every client: moving someone's playback to a point,
 * the title a remembered playback problem belongs to, whose a tuner is, a source's own import
 * limits, the subtitle provider's account, what an administrator may do, and merging titles on
 * a phone. Merged into `enUS` statically.
 */
export const ownerParityMessages = {
  // ── Watching now: a position bar for playback on another device ──
  'web.nowPlaying.positionOf': 'At {position} of {duration}',
  'web.nowPlaying.moveTo': 'Move to…',
  'web.nowPlaying.moveTitle': 'Move “{title}”',
  'web.nowPlaying.moveBody': 'Playback on {viewer}’s device jumps to the point you choose.',
  'web.nowPlaying.moveConfirm': 'Move here',
  // ── Streaming › Playback problems ──
  'server.routes.unknownTitle': 'A file no longer in a library',
  'server.routes.openTitle': 'Open the title',
  // ── Live TV › Recording › Tuners ──
  'server.tuners.whoseProfile': '{profile} ({account})',
  // ── Live TV › a source › Import limits ──
  'server.liveSource.limits': 'Import limits for this source',
  'server.liveSource.limitsHelp': 'Lower than the server’s limits for this source alone. Leave a field empty to use the server’s.',
  'server.liveSource.limitDefault': 'Server: {value}',
  // ── Metadata › Subtitles found online ──
  'server.metadata.subtitles': 'Subtitles found online',
  'server.metadata.subtitlesLede': 'Portico searches OpenSubtitles.com with your own free account and API key. Create an account at opensubtitles.com, then add an API consumer in your profile there to get a key.',
  'server.metadata.openSubtitlesGetKey': 'Get a key at OpenSubtitles.com',
  'server.metadata.openSubtitlesKey': 'OpenSubtitles API key',
  'server.metadata.openSubtitlesUser': 'OpenSubtitles username',
  'server.metadata.openSubtitlesPassword': 'OpenSubtitles password',
  'server.metadata.openSubtitlesAccountHelp': 'OpenSubtitles lets signed-in accounts download, and counts downloads per account each day.',
  'server.metadata.openSubtitlesKeyOwn': 'Your key is saved',
  'server.metadata.openSubtitlesKeyEnvironment': 'Set in the server’s environment',
  'server.metadata.openSubtitlesKeyNone': 'No key yet',
  'server.metadata.openSubtitlesAccountEnvironment': 'An account is set in the server’s environment',
  'server.metadata.openSubtitlesPasswordSaved': 'A password is saved',
  'server.metadata.openSubtitlesRemoveKey': 'Remove my key',
  'server.metadata.openSubtitlesSignOut': 'Remove the account',
  // The connection: set up, working, refused and why, downloads left today.
  'server.metadata.openSubtitlesConnection': 'Connection',
  'server.metadata.openSubtitlesTest': 'Test connection',
  'server.metadata.openSubtitlesTesting': 'Testing…',
  'server.metadata.openSubtitlesTestUnsaved': 'Save your changes, then test them.',
  'server.metadata.openSubtitlesNotSetUp': 'Not set up. Add your OpenSubtitles API key to search for subtitles.',
  'server.metadata.openSubtitlesNotTested': 'Not tested yet',
  'server.metadata.openSubtitlesSignedIn': 'Signed in and working',
  'server.metadata.openSubtitlesKeyWorks': 'The key works. Add your account too, so Portico can download.',
  'server.metadata.openSubtitlesKeyRejected': 'OpenSubtitles didn’t accept the API key. Check it, save, and test again.',
  'server.metadata.openSubtitlesAccountRejected': 'OpenSubtitles didn’t accept the username or password. Check them, save, and test again.',
  'server.metadata.openSubtitlesNeedsAccount': 'Searching works, but OpenSubtitles only lets signed-in accounts download. Add your username and password.',
  'server.metadata.openSubtitlesNoPassword': 'Add the account’s password to sign in.',
  'server.metadata.openSubtitlesQuotaUsed': 'Today’s downloads are used up. They start again at {time}.',
  'server.metadata.openSubtitlesSlowDown': 'OpenSubtitles asked Portico to wait until {time}.',
  'server.metadata.openSubtitlesLater': 'OpenSubtitles asked Portico to wait. Try again later.',
  'server.metadata.openSubtitlesUnreachable': 'OpenSubtitles couldn’t be reached. Test again in a little while.',
  'server.metadata.openSubtitlesDownloadsOf': '{remaining} of {allowed, plural, one {# download} other {# downloads}} left today',
  'server.metadata.openSubtitlesDownloadsLeft': '{remaining, plural, one {# download} other {# downloads}} left today',
  // ── Profiles: forgetting the devices that remember one ──
  'profile.devicesForgotten': 'Devices that remembered {name} will ask for its PIN again.',
  // ── Merging titles from a phone ──
  'title.merge.find': 'Title to merge with',
  'title.merge.none': 'No other title matches.',
  'title.merge.action': 'Merge with another title…',
  // ── Owner wording on phones and tablets: each says what it is, not the nearest phrase ──
  'api.tokens.share': 'Share or copy the token',
  'api.webhooks.shareSecret': 'Share or copy the secret',
  'api.mcp.shareAddress': 'Share or copy the address',
  'server.reports.share': 'Share this report',
  'api.webhooks.off': 'Off',
  'server.metadata.keyUnsaved': 'Your new key is used once you save.',
  'server.dashboard.freeSpace': 'Free space',
  'server.dashboard.porticoFiles': 'Portico’s own files',
  'server.people.hoursTimeHelp': 'Type a time, such as 8:30 or 20:15.',
} as const;
