import type {IconName} from '../../../design/src/next-icons.ts';

/**
 * One navigation glyph table for actual library kinds, never translated names.
 * The current server creates movie/tv/anime/music/audiobook libraries. The read
 * presentation keys book/books/comics are explicit forward-compatible inputs;
 * this helper never offers a library or grants access to it.
 * Unknown kinds keep the established film-family fallback (content.iconFor).
 */
export function libraryNavigationIcon(kind: string, platform: 'touch' | 'tv'): IconName {
  switch (kind) {
    case 'tv': return 'i-tvlib';
    case 'anime': return 'i-category';
    case 'music': return 'i-music';
    case 'audiobook': return platform === 'touch' ? 'i-headphones' : 'i-book';
    case 'book': case 'books': return 'i-book';
    case 'comics': return 'i-panels';
    case 'movie': default: return 'i-film';
  }
}
