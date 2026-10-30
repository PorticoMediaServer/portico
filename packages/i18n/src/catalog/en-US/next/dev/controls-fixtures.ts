/** Imported only by the serve-only controls family, never by a production aggregate. */
export const controlsFixtureMessages = {
  'next.controls.fixture.remaining': '1 h 26 min left',
  'next.controls.fixture.remainingGlass': '1 h 12 min left',
  'next.controls.fixture.season': 'Season 1',
  'next.controls.fixture.longName': 'The Lord of the Rings: The Fellowship of the Ring',
  'next.controls.fixture.server': 'Portico demo',
  'next.controls.fixture.code': 'KQ7M 2XHD',
  'next.controls.fixture.settingsSearch': 'subtitle',
  'next.controls.fixture.qualityLabel': 'Quality: automatic, 1080p now',
} as const satisfies Record<`next.controls.fixture.${string}`, string>;
