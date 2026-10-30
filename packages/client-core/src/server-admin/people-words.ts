/**
 * Words the People pages share: a role, a device's platform, and the line under a device's name.
 * No rendering; the web and the phone app draw the same sentences.
 */
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import type {AccessDevice, AccessTier} from '../server-administration.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;

const ROLE_IDS: Readonly<Record<AccessTier, MessageId>> = {owner: 'web.people.ownerBadge', admin: 'web.access.roleAdmin', member: 'web.access.roleMember'};
/** "Owner", "Administrator" or "Member", in the viewer's language. */
export const roleLabel = (t: Translate, role: AccessTier): string => t(ROLE_IDS[role] ?? 'web.access.roleMember');

const PLATFORM_IDS: Readonly<Record<string, MessageId>> = {
  web: 'device.platform.web', ios: 'device.platform.ios', tvos: 'device.platform.tvos', android: 'device.platform.android', androidtv: 'device.platform.androidtv',
  api: 'device.platform.api',
  'google-cast': 'device.platform.googleCast',
};
/** What kind of device it is, in words; nothing for a platform this client has no word for (never the raw code). */
export const devicePlatform = (t: Translate, platform: string): string => (PLATFORM_IDS[platform] ? t(PLATFORM_IDS[platform]!) : '');

/** The line under a device: whose it is (when the list is everyone's), what it is, and when it was last here. Parts that are not known are left out. */
export function deviceFacts(t: Translate, device: Pick<AccessDevice, 'username' | 'platform' | 'lastSeenAt'>, options: Readonly<{showOwner: boolean; relative: (iso: string) => string}>): string {
  return [
    options.showOwner ? device.username : '',
    devicePlatform(t, device.platform),
    device.lastSeenAt ? t('web.devices.lastSeen', {when: options.relative(device.lastSeenAt)}) : '',
  ].filter(Boolean).join(' · ');
}

// ── Viewing hours: the days of one window, as a choice an owner makes in one control ───────

export type DayChoice = 'all' | 'weekdays' | 'weekends' | 'several' | '0' | '1' | '2' | '3' | '4' | '5' | '6';
const DAY_SETS: Readonly<Record<'all' | 'weekdays' | 'weekends', readonly number[]>> = {all: [0, 1, 2, 3, 4, 5, 6], weekdays: [1, 2, 3, 4, 5], weekends: [0, 6]};
const sameDays = (a: readonly number[], b: readonly number[]) => a.length === b.length && [...a].sort().join() === [...b].sort().join();

/** Which choice a window's days are: every day, weekdays, weekends, one day, or a set saved elsewhere that the control keeps as it is. */
export function dayChoiceOf(days: readonly number[]): DayChoice {
  const unique = [...new Set(days)].filter(d => Number.isInteger(d) && d >= 0 && d <= 6);
  for (const key of ['all', 'weekdays', 'weekends'] as const) if (sameDays(unique, DAY_SETS[key])) return key;
  return unique.length === 1 ? (String(unique[0]) as DayChoice) : 'several';
}
/** The days a choice stands for; `several` keeps the days it was given. */
export function daysOfChoice(choice: DayChoice, current: readonly number[]): readonly number[] {
  if (choice === 'several') return current;
  return choice === 'all' || choice === 'weekdays' || choice === 'weekends' ? DAY_SETS[choice] : [Number(choice)];
}
/** The hours a new window starts with: every day, eight in the morning until eight in the evening. */
export const newViewingHours = (): Readonly<{days: readonly number[]; startMinute: number; endMinute: number}> => ({days: DAY_SETS.all, startMinute: 8 * 60, endMinute: 20 * 60});
/** The server keeps at most this many windows for one account. */
export const VIEWING_HOURS_MAX = 64;
