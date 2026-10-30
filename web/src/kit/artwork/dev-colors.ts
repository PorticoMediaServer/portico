// Exact reference palette values. Imported only by the serve-only artwork gallery.
export const referenceArtworkColors = {
  palette: {first: '#2f86bb', second: '#1e5b52'},
  gold: {first: '#a8892a', second: '#5a3a1a'},
  teal: {first: '#2e6a6f', second: '#1f4f7a'},
  red: {first: '#8f2e24', second: '#3f2150'},
  green: {first: '#3f8f6e', second: '#153749'},
  brown: {first: '#b4642a', second: '#5e2a22'},
  slate: {first: '#55709a', second: '#2a3a66'},
  channel: '#286383',
  // auth.html 13/15/16/22 inline bases, deliberately different from owner-people's semantic map.
  authProfileTints: {demo: '#2f86bb', jules: '#8a7a3a'} as Readonly<Record<string, string>>,
} as const;
