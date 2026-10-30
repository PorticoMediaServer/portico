/**
 * "Play on" destinations (X-18, as simplified by Justin on 22 Sep): one ordered model for every
 * client.
 *
 * - AirPlay, where the platform has the system picker: iPhone, iPad, and Safari on macOS.
 * - Google Cast: Chromecasts, through the Cast SDK's own picker, where the SDK is available.
 * - Portico devices on this network: signed-in Portico players found by local-network discovery
 *   only (Bonjour/mDNS). Never a cloud or remote device list, and never a code to type.
 * - Televisions (Apple TV, Android TV, Fire TV, including Fire TV on Vega OS) offer no casting UI
 *   at all: a TV is a receiver.
 *
 * With nothing to offer, the sheet says so (`empty`) rather than showing a dead list.
 */
import type {IconId} from '../../../design/src/icons.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

export type DestinationPlatform = 'ios' | 'ipados' | 'android' | 'web' | 'tvos' | 'androidtv' | 'firetv' | 'vega';

/** A signed-in Portico player found on the local network. */
export type NearbyPlayer = Readonly<{id: string; name: string; kind: 'tv' | 'phone' | 'tablet' | 'computer'}>;

export type DestinationRow =
  | Readonly<{kind: 'airplay'; label: MessageId; subtitle: MessageId; icon: IconId}>
  | Readonly<{kind: 'googleCast'; label: MessageId; icon: IconId}>
  | Readonly<{kind: 'porticoDevice'; id: string; name: string; icon: IconId}>;

export type Destinations = Readonly<{
  /** False on televisions: show no "Play on" entry at all. */
  offered: boolean;
  /** Rows in display order. */
  rows: readonly DestinationRow[];
  /** Heading above the Portico devices, when there are any. */
  nearbyHeading?: MessageId;
  /** Shown instead of rows when there is nothing to play on. */
  empty?: MessageId;
}>;

export type DestinationCapabilities = Readonly<{
  /** The system AirPlay picker exists here (iOS/iPadOS; web only in Safari on macOS). */
  airplay?: boolean;
  /** The Google Cast SDK is available (on web, a Chromium browser with the sender loaded). */
  googleCast?: boolean;
  /** Portico players discovered on the local network (and signed in). */
  nearby?: readonly NearbyPlayer[];
}>;

const playerGlyph: Record<NearbyPlayer['kind'], IconId> = {tv: 'tvDevice', phone: 'phone', tablet: 'tablet', computer: 'browser'};

export function playOnDestinations(platform: DestinationPlatform, c: DestinationCapabilities = {}): Destinations {
  if (platform === 'tvos' || platform === 'androidtv' || platform === 'firetv' || platform === 'vega') return Object.freeze({offered: false, rows: []});
  const rows: DestinationRow[] = [];
  if (c.airplay) rows.push({kind: 'airplay', label: 'playOn.airplay', subtitle: 'playOn.airplaySubtitle', icon: 'airplay'});
  if (c.googleCast) rows.push({kind: 'googleCast', label: 'playOn.googleCast', icon: 'googleCast'});
  for (const p of c.nearby ?? []) rows.push({kind: 'porticoDevice', id: p.id, name: p.name, icon: playerGlyph[p.kind] ?? 'tvDevice'});
  return Object.freeze({
    offered: true,
    rows: Object.freeze(rows),
    ...(c.nearby?.length ? {nearbyHeading: 'playOn.onThisNetwork' as MessageId} : {}),
    ...(rows.length ? {} : {empty: 'playOn.noneNearby' as MessageId}),
  });
}

/** Web capability detection, from the browser globals (no DOM types needed here). */
export function webDestinationSupport(env: {chromium?: boolean; castApi?: boolean; safariAirPlay?: boolean}): Pick<DestinationCapabilities, 'airplay' | 'googleCast'> {
  return {googleCast: !!env.chromium && !!env.castApi, airplay: !!env.safariAirPlay};
}
