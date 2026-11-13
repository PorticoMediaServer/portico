import {nextIcons, type IconName} from '../../../../../packages/design/src/next-icons';

/** The settings structure names its icons from the design registry (`IconId`). A few of those have another name in the
 * app's glyph set; without this they fell back to the slider glyph, so Audio & subtitles and This device drew
 * Appearance's icon (owner, 13 Nov). The glyphs are the settings spec's (`design/ui-v1/settings/spec`). */
const glyphFor: Readonly<Record<string, IconName>> = {subtitles: 'i-cc', tvDevice: 'i-monitor', profile: 'i-user', transcode: 'i-swap', network: 'i-wifi', calendar: 'i-cal'};

/** As settings.html: strip one leading i-, look the name up, fall back to sliders. */
export function nativeSettingsIcon(name: string): IconName {
  const bare = name.replace(/^i-/, ''), key = `i-${bare}`;
  return glyphFor[bare] ?? (Object.hasOwn(nextIcons, key) ? key as IconName : 'i-sliders');
}
