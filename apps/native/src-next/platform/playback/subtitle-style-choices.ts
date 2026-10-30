import {sameFence, samePlayback, type PlaybackFence} from '../../../../../packages/player-core/src/identity';
import type {SubtitlePreferences} from '../../../../../packages/client-core/src/preference-effects';

const fields = {
  'playback.subtitleSize': {name: 'size', values: ['small', 'medium', 'large', 'extra-large']},
  'playback.subtitleColor': {name: 'color', values: ['white', 'yellow', 'cyan', 'green', 'pink']},
  'playback.subtitleBackground': {name: 'background', values: ['none', 'outline', 'shadow', 'translucent', 'opaque']},
  'playback.subtitlePosition': {name: 'position', values: ['bottom', 'raised', 'top']},
  'playback.subtitleFont': {name: 'font', values: ['default', 'serif', 'mono', 'rounded']},
} as const;
export type NativeSubtitleStyleKey = keyof typeof fields;
export const nativeSubtitleStyleKeys = Object.freeze(Object.keys(fields) as NativeSubtitleStyleKey[]);
export const nativeSubtitleStyleValue = (key: NativeSubtitleStyleKey, preferences: SubtitlePreferences): string => preferences[fields[key].name];

/** Appearance belongs to the current playback run, never a title or a saved
 * preference document. A retained panel can only change its own current source. */
export class NativeSubtitleStyleChoices {
  private fence?: PlaybackFence;
  private overrides: Partial<SubtitlePreferences> = {};
  bind(fence: PlaybackFence): void {
    if (!samePlayback(this.fence?.playback, fence.playback)) this.overrides = {};
    this.fence = fence;
  }
  clear(): void { this.fence = undefined; this.overrides = {}; }
  read(fence: PlaybackFence, defaults: SubtitlePreferences): SubtitlePreferences {
    return Object.freeze({...defaults, ...(samePlayback(this.fence?.playback, fence.playback) ? this.overrides : {})});
  }
  set(fence: PlaybackFence, key: string, value: string): boolean {
    if (!sameFence(this.fence, fence)) return false;
    if (!Object.hasOwn(fields, key)) throw new Error('invalid_playback_preference');
    const field = fields[key as NativeSubtitleStyleKey];
    if (!(field.values as readonly string[]).includes(value)) throw new Error('invalid_playback_preference');
    this.overrides = {...this.overrides, [field.name]: value};
    return true;
  }
}
