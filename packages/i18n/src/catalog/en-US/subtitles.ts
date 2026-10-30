/**
 * en-US messages for subtitles the player draws itself (styled and image subtitles) and for the
 * "up next" card shown during the credits, and for subtitles found online. Merged into `enUS`
 * statically like `web.ts`.
 */
export const drawnSubtitleMessages = {
  // ── Subtitles found online: the attribution OpenSubtitles asks for, and its refusals ──
  'player.opt.providedByOpenSubtitles': 'Subtitles provided by OpenSubtitles.com',
  'subtitles.error.providerNotConfigured': 'Subtitle search isn’t set up on this server. Ask the owner to add an OpenSubtitles key.',
  'subtitles.error.providerKeyRejected': 'OpenSubtitles didn’t accept this server’s key. Ask the owner to check it.',
  'subtitles.error.providerSignInFailed': 'OpenSubtitles didn’t accept this server’s account. Ask the owner to check it.',
  'subtitles.error.providerSignInRequired': 'OpenSubtitles only lets signed-in accounts download. Ask the owner to add an account.',
  'subtitles.error.providerQuota': 'This server has used today’s OpenSubtitles downloads. They start again at {time}.',
  'subtitles.error.providerQuotaLater': 'This server has used today’s OpenSubtitles downloads. Try again tomorrow.',
  'subtitles.error.providerBusy': 'OpenSubtitles is busy. Try again in a moment.',
  'player.subtitles.position': 'Position',
  'player.subtitles.positionHelp': 'Move subtitles up or down the picture',
  'player.subtitles.moveUp': 'Up',
  'player.subtitles.moveDown': 'Down',
  'player.subtitles.positionReset': 'Reset',
  'player.subtitles.positionNotice': 'Subtitles moved {direction}',
  'player.subtitles.positionUp': 'up',
  'player.subtitles.positionDown': 'down',
  'player.subtitles.positionBack': 'Subtitles back where they were made to sit',
  'player.subtitles.loadingDrawn': 'Loading subtitles…',
  'player.subtitles.drawFailed': 'These subtitles could not be drawn on this device, so they are being shown another way.',
  'player.subtitles.drawUnavailable': 'These subtitles could not be shown.',
  'next.subtitles.image.noPictures': 'This subtitle has no pictures that can be shown.',
  'next.subtitles.image.unreadable': 'This subtitle could not be read.',
  'player.subtitles.forcedOnly': 'Forced only',
  'player.subtitles.forcedOnlyHelp': 'Show only the lines marked forced: foreign dialogue and signs',
  'player.upNext.duringCredits': 'Up next',
  'player.upNext.playNow': 'Play now',
  'player.upNext.watchCredits': 'Watch credits',
  'player.upNext.notNow': 'Not now',
  'player.upNext.startsIn': 'Starts in {time}',
  'player.upNext.startsAfterScene': 'Starts in {time}, after the scene',
  'player.upNext.sceneAfterCredits': "There's a scene after the credits. Skip credits takes you straight to it.",
  'player.upNext.playingIn': 'Playing in {seconds}s',
  'player.ended.outsideHoursTitle': 'Viewing hours are over',
  'player.ended.outsideHours': 'This account can’t be used at this time of day, so playback stopped. Try again during its allowed hours.',
} as const satisfies Record<string, string>;
