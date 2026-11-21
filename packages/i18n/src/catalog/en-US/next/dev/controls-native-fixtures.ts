/** Imported only by the native development gallery; never registered in nextMessages. */
export const nativeControlsFixtureMessages = {
  'next.controls.native.fixture.longTitle': 'The Lord of the Rings: The Fellowship of the Ring',
  // The gallery's permanent long-content cases: a 70-character title, a long server address and
  // name, a long username and a translation-length label.
  'next.controls.native.fixture.title70': 'The Extraordinarily Long Night the Last Ferry Disappeared Into the Fog',
  'next.controls.native.fixture.longAddress': 'https://media-server.livingroom.example-household-network.home.arpa:32400',
  'next.controls.native.fixture.longServerName': 'Montgomery-Fitzwilliam Family Living Room Media Server',
  'next.controls.native.fixture.cottageServer': 'The cottage',
  'next.controls.native.fixture.familyServer': 'Dad’s server',
  'next.controls.native.fixture.lastSeenOctober': '12 Oct',
  'next.controls.native.fixture.lastSeenSeptember': '3 Sep',
  'next.controls.native.fixture.longUsername': 'alexandra.montgomery-fitzwilliam@family-household.example',
  'next.controls.native.fixture.translatedLabel': 'Wiedergabeeinstellungen für alle Profile übernehmen',
  'next.controls.native.fixture.serverName': 'Living Room',
  'next.controls.native.fixture.username': 'demo-member',
  'next.controls.native.fixture.password': 'correct horse',
} as const satisfies Record<`next.controls.native.fixture.${string}`, string>;
