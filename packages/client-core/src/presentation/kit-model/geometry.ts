import type {TitleKind} from './facts.ts';

/** Read is a local presentation vocabulary; it does not extend catalogue wire kinds. */
export type GeometrySubject =
  | Readonly<{domain: 'catalog'; kind: TitleKind; family?: 'video' | 'music' | 'audiobook'}>
  | Readonly<{domain: 'person'}>
  | Readonly<{domain: 'channel'}>
  | Readonly<{domain: 'read'; kind: 'book' | 'comic' | 'magazine' | 'newspaper'}>;
export type GeometryContext = 'ordinary' | 'continuation' | 'in-show' | 'search' | 'detail-extra' | 'collection-banner';
export type CardGeometry = Readonly<{shape: 'poster' | 'square' | 'circle' | 'wide' | 'periodical'; widthOverHeight: number}> | Readonly<{shape: 'banner'}>;

export function cardGeometry(subject: GeometrySubject, context: GeometryContext): CardGeometry {
  if (context === 'collection-banner' && subject.domain === 'catalog' && subject.kind === 'collection') return Object.freeze({shape: 'banner'});
  if (subject.domain === 'person') return Object.freeze({shape: 'circle', widthOverHeight: 1});
  if (subject.domain === 'channel') return Object.freeze({shape: 'wide', widthOverHeight: 16 / 9});
  if (subject.domain === 'read') return Object.freeze(subject.kind === 'magazine' || subject.kind === 'newspaper' ? {shape: 'periodical', widthOverHeight: 3 / 4} : {shape: 'poster', widthOverHeight: 2 / 3});
  if (subject.kind === 'artist' || subject.kind === 'author') return Object.freeze({shape: 'circle', widthOverHeight: 1});
  if (['song', 'album', 'disc', 'book', 'audiobook_file', 'chapter', 'book_series'].includes(subject.kind) || (subject.kind === 'collection' || subject.kind === 'category') && (subject.family === 'music' || subject.family === 'audiobook')) return Object.freeze({shape: 'square', widthOverHeight: 1});
  if (context === 'continuation' || subject.kind === 'episode' && (context === 'in-show' || context === 'search') || subject.kind === 'extra' && context === 'detail-extra') return Object.freeze({shape: 'wide', widthOverHeight: 16 / 9});
  return Object.freeze({shape: 'poster', widthOverHeight: 2 / 3});
}

/** A renderer cannot assume one ordinary row can mix poster, circle, and square cards. */
export function hasUniformGeometry(geometries: readonly CardGeometry[]): boolean {
  return geometries.every(g => g.shape === geometries[0]?.shape);
}
