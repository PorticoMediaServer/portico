import type {CapabilityOverrides} from '@core/client-profile.ts';

/**
 * The "if something looks or sounds wrong" switches (Settings › Quality): kept by this browser,
 * and stamped on the capability document it publishes, so the server plans playback for this
 * device as if it could not do the thing that is going wrong. Never account state.
 */
export type PlaybackFixes = Required<CapabilityOverrides>;
const key = 'portico.playbackFixes.v1';
const changed = 'portico:playback-fixes';
const none: PlaybackFixes = Object.freeze({forceStereo: false, disablePassthrough: false, disableHdr: false});

/** What a stored value reads as: only a literal `true` turns a switch on. */
export function parsePlaybackFixes(raw: unknown): PlaybackFixes {
  const v = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {};
  return {forceStereo: v.forceStereo === true, disablePassthrough: v.disablePassthrough === true, disableHdr: v.disableHdr === true};
}

export function readPlaybackFixes(): PlaybackFixes {
  try { const raw = localStorage.getItem(key); return raw ? parsePlaybackFixes(JSON.parse(raw)) : none; } catch { return none; }
}

/** Saves a change and tells the page, so the capability document is published again. */
export function writePlaybackFixes(patch: Partial<PlaybackFixes>): PlaybackFixes {
  const next = parsePlaybackFixes({...readPlaybackFixes(), ...patch});
  try { localStorage.setItem(key, JSON.stringify(next)); } catch { /* a browser that keeps nothing still applies it until the page closes */ }
  held = next;
  try { window.dispatchEvent(new Event(changed)); } catch { /* no window: nothing to tell */ }
  return next;
}

// What was last written in this page, for a browser whose storage refuses writes.
let held: PlaybackFixes | undefined;
export const currentPlaybackFixes = (): PlaybackFixes => held ?? readPlaybackFixes();

/** Calls back when a switch changes; returns the way to stop. */
export function onPlaybackFixesChanged(listener: () => void): () => void {
  window.addEventListener(changed, listener);
  return () => window.removeEventListener(changed, listener);
}
