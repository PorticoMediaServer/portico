import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {AVOIDS} from '../../packages/i18n/scripts/lint-glossary.mjs';
import {nextMessages} from '../../packages/i18n/src/catalog/en-US/next/index.ts';
// Exact approved state copy supersedes the legacy glossary for these messages only.
// states.html:1136 (updated server), :1154 (player details), :1619 (crash recovery).
const approvedStateCopy = {
  'next.overlays.reloadHelp': 'Reload to use the new version.',
  'next.overlays.reload': 'Reload',
  'next.states.reload': 'Reload',
  // states.html#24 (crash recovery): the web's last-resort page reloads the app itself,
  // where states.html#25 (the phone's) says Try again.
  'next.states.reloadPage': 'Reload',
  'next.states.playbackInfo': 'Playback info',
  // playback-info.html:735–736 names this same panel, not a title's media details.
  'next.info.title': 'Playback info',
};
// collections.html 24a/c/e/f and 26b explicitly use Reload for a conflicted Home layout.
const approvedHomeCopy = {
  'next.home.layoutChangedBody': 'Your Home layout changed on another device after you opened this. Nothing was overwritten. Reload it to see the latest, then make your changes again.',
  'next.home.layoutChangedBodyCompact': 'Your Home layout changed on another device after you opened this. Nothing was overwritten. Reload it, then make your changes again.',
  'next.home.reloadLayout': 'Reload layout',
};
// Settings 38b51b9 supersedes the older flat glossary at these exact rows only.
const approvedSettingsCopy = {
  'next.settingsSpec.account.quality.advanced.groups.1.rows.0.help': 'Direct play. Best picture, no server cost.',
  'next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.2.control.options.2.hint': 'Reads every file in full. Slower.',
  'next.settingsSpec.server.libraries.panels.library.tabs.3.groups.1.rows.1.control.options.1.hint': 'Slower scans',
  'next.settingsSpec.server.segments.advanced.groups.1.rows.1.control.options.1.hint': 'Slower, finds late intros',
  'next.settingsSpec.server.segments.advanced.groups.1.rows.2.control.options.1.hint': 'Slower, finds long credits',
  'next.settingsSpec.server.segments.advanced.groups.1.rows.3.control.options.1.hint': 'Slower, finds scenes after the credits',
  'next.settingsSpec.server.integrations.advanced.groups.0.rows.1.help': 'Slower answers count as not arrived.',
  'next.settingsSpec.server.integrations.advanced.groups.1.rows.2.label': 'Member token scope',
  'next.settingsSpec.server.streaming.advanced.groups.2.rows.0.help': 'Slower is smaller and cleaner, but heavier.',
  'next.settingsSpec.server.storage.groups.0.rows.3.help': 'Pages and videos start slower for a while.',
  'next.settingsSpec.server.storage.groups.0.rows.3.control.confirm': 'Empty the cache? Some pages and videos start slower while Portico rebuilds it. Nothing you added is lost.',
};
// Exact context matters: these are Reading locations, sign-in mode data,
// and a diagnostic log noun, rather than the legacy glossary's saved-title/action senses.
// desktop-read.html:1041; settings.html:1140. No broad key or substring exemption.
const contextCopy = {
  'next.reader.bookmarks': 'Bookmarks',
  'next.tvPeople.local': 'Local account',
  'next.settings.librarySonic.retryUnavailable': 'Still can’t load. The log in Troubleshooting has the reason.',
};
export function lintNextGlossary(messages) {
  const rules = [...AVOIDS, [/Portico Cloud/, 'Portico Services']];
  return Object.entries(messages).flatMap(([key, value]) => rules.filter(([pattern]) => pattern.test(value) && approvedStateCopy[key] !== value && approvedHomeCopy[key] !== value && approvedSettingsCopy[key] !== value && contextCopy[key] !== value).map(([, expected]) => `${key}: use ${expected}`));
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const failures = lintNextGlossary(nextMessages);
  if (failures.length) { console.error(failures.join('\n')); process.exit(1); }
  console.log('next glossary clean');
}
