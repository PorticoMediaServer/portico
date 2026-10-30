import type {ArtworkSource} from '../../kit/artwork';
import {generatedCoverTint} from '@design';
import type {ReadCoverItem} from './ReadLibrary';

/**
 * One item of the server's recently-added row (GET /v1/reading/discover). The
 * row is the catalogue's own added_ms order: this module never re-sorts it and
 * never invents a date the DTO does not carry.
 */
export type RecentReadingItem = Readonly<{
  id: string;
  sourceId: string;
  title: string;
  coverUrl?: string;
  author?: Readonly<{value: string}>;
  inventoryAvailable: boolean;
}>;

/**
 * phone-read 1: the Recently added shelf uses the same bounded cover
 * presentation as the library grid, at the handheld scale.
 */
export function recentCards(
  items: readonly RecentReadingItem[],
  actions: Readonly<{artwork?: (url: string | undefined) => ArtworkSource | undefined; kind?: 'book' | 'comic'; read: string; open: (item: RecentReadingItem) => void; play: (item: RecentReadingItem) => void}>,
): readonly ReadCoverItem[] {
  return items.map(item => ({id: item.id, card: {
    kind: actions.kind ?? 'book',
    artwork: actions.artwork?.(item.coverUrl),
    context: 'shelf',
    density: 'shelf',
    model: {
      title: item.title,
      caption: item.author ? [{id: 'author', text: item.author.value, role: 'identity' as const}] : [],
    },
    generated: {title: item.title, glyph: Array.from(item.title)[0] ?? '', tint: generatedCoverTint(item.title)},
    visible: true,
    disabled: !item.inventoryAvailable,
    playLabel: actions.read,
    onNavigate: () => actions.open(item),
    onPlay: item.inventoryAvailable ? () => actions.play(item) : undefined,
  }}));
}
