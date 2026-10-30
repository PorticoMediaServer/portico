/** Reference-only catalogue facts. This module has no production aggregate import. */
export const dockNativeFixtures = {
  video: {title: 'Slow Horses', episode: "Failure's Contagious", next: 'Work Drinks'},
  music: {title: 'Too Late', artist: 'The Weeknd', album: 'After Hours'},
  tvMusic: {title: 'DNA.', artist: 'Kendrick Lamar', album: 'DAMN.', year: '2017'},
  book: {title: 'The Martian', author: 'Andy Weir'},
  device: 'Living room TV',
} as const;
