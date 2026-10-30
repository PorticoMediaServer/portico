import {launchStartedAt} from '../platform/boot-host';

/** Cold-launch milestones, each logged once per process in release builds too (P2-74 round 4b:
 * Home painted at +6.0 s although its data was in by +4.3 s). Offsets from the platform's launch
 * time and step names only. */
const logged = new Set<string>();
export type BootMilestone = 'restored' | 'boot-hidden' | 'home-painted';

export function logBootMilestone(event: BootMilestone, detail?: Record<string, unknown>, sink: (line: string) => void = line => console.warn(line)) {
  if (logged.has(event)) return;
  logged.add(event);
  const launched = launchStartedAt();
  try {sink(`portico_boot_timing ${JSON.stringify({event, sinceLaunchMs: launched === undefined ? null : Date.now() - launched, ...detail})}`);} catch { /* passive */ }
}
