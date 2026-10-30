/** Match the people circles: the first Unicode character of the first two words. */
export function audiobookAuthorInitials(name: string): string {
  return name.split(/\s+/u).filter(Boolean).slice(0, 2).map(word => [...word][0]).join('');
}
