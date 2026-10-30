export const artworkNativeMessages = {
  'next.artwork.native.galleryTitle': 'When the artwork isn’t there',
  'next.artwork.native.lead': 'The demo library has no title logos, so the title in large type is the standard treatment rather than a fallback. Each hero below uses only what that title actually has on the demo server.',
  'next.artwork.native.backdrop': 'Backdrop',
  'next.artwork.native.posterOnly': 'Poster only',
  'next.artwork.native.noArtwork': 'No artwork',
  'next.artwork.native.manageProfiles': 'Manage profiles',
} as const satisfies Record<`next.artwork.native.${string}`, string>;
