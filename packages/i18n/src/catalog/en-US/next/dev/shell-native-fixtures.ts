/** Reference domain values. Imported directly by the DEV-only native shell galleries. */
export const shellNativeFixtures = {
  serverName: 'Portico demo',
  profileName: 'Demo',
  ownerInitial: 'D',
  memberName: 'Sam',
  memberInitial: 'S',
  administratorName: 'Priya',
  administratorInitial: 'P',
  running: 'Running normally',
  uptime: 'Up for 6 days',
  free: '1.2 TB free',
  database: 'Database 412 MB',
} as const;

export const shellNativeCases = {
  profiles: [
    {id: 'demo', name: 'Demo', initial: 'D', color: 'blue', primary: true},
    {id: 'sam', name: 'Sam', initial: 'S', color: 'mint', pinRequired: true},
    {id: 'kids', name: 'Kids', initial: 'K', color: 'gold', unavailableReason: 'restricted'},
  ],
  currentProfileId: 'demo',
  group: {name: 'Movie night', status: 'In step', waiting: false},
  groupStates: [{status: 'In step', waiting: false}, {status: 'Waiting for Sam…', waiting: true}, {status: 'Reconnecting…', waiting: true}, {status: 'Host reconnecting', waiting: true}],
  notification: {id: 'reference-download-ready', title: 'Download ready', body: 'Work Drinks finished downloading and is ready to play offline.', when: '12 min ago', read: false, archived: false, severity: 'info'},
  avatarSubject: {uri: 'http://100.120.50.24:8787/assets/pool/tv/severance.jpg', cacheKey: 'reference-severance-subject'},
  subtitleSource: {uri: 'http://100.120.50.24:8787/assets/pool/movies/dune-part-two--bd.jpg', cacheKey: 'reference-dune-backdrop'},
  languages: [{id: 'en', label: 'English'}, {id: 'fr', label: 'French'}, {id: 'es-419', label: 'Spanish (Latin America)'}],
  availableLanguages: [{id: 'en', label: 'English'}, {id: 'fr', label: 'French'}, {id: 'es-419', label: 'Spanish (Latin America)'}, {id: 'de', label: 'German'}],
  defaultSkip: 'ask',
  defaultMethod: 'automatic',
  subtitleSize: 'medium',
  subtitleBackground: 'translucent',
  stats: [
    {id: 'cpu', label: 'CPU', value: '12%', samples: [7, 8, 12, 6, 10, 8, 13, 9, 12], min: 0, max: 20},
    {id: 'memory', label: 'Memory', value: '1.2 GB', samples: [1, 1.05, 1.07, 1.1, 1.09, 1.15, 1.2], min: 0, max: 2},
    {id: 'upload', label: 'Upload', value: '8.2 Mbps', samples: [4, 6, 5, 8, 7, 9, 8.2], min: 0, max: 12},
  ],
  barRows: [{id: 'tlou', label: 'The Last of Us', value: '18', fraction: 1}, {id: 'severance', label: 'Severance', value: '12', fraction: 2 / 3}],
  person: {id: 'sam', name: 'Sam', face: {initial: 'S', color: 'mint'}, facts: [{id: 'signin', text: 'Server account', role: 'context'}, {id: 'profiles', text: '3 profiles', role: 'value'}], activity: {text: 'Watching now', playing: true}},
  device: {id: 'living-room', name: 'Living room TV', icon: 'i-tv', facts: [{id: 'person', text: 'Sam', role: 'identity'}, {id: 'platform', text: 'Android TV', role: 'context'}, {id: 'network', text: 'Home network', role: 'context'}], state: 'Waiting for your approval', handoff: 'Approve or block this device on your phone or computer.'},
  waitingMessage: '1 device is waiting for your approval.',
  segment: {itemId: 'reference-severance-s1e3', title: 'In Perpetuity', number: 3, durationUS: '3240000000', state: 'complete', errorCode: '', sourceId: 'reference-severance-file', sourceRevision: '1', mappingRevision: '1', markerRevision: 1, markers: [
    {id: 'reference-intro', kind: 'intro', startUS: '38000000', endUS: '108000000', confidence: .96, provenance: 'reference', basis: 'season', approved: false, edited: false, automaticSafe: false, viewerVisible: true},
    {id: 'reference-credits', kind: 'credits', startUS: '3108000000', endUS: '3230000000', confidence: .98, provenance: 'reference', basis: 'chapter', approved: true, edited: false, automaticSafe: true, viewerVisible: true},
  ]},
  windows: [{id: 'opening', label: 'Opening', startUS: '0', endUS: '180000000'}, {id: 'end', label: 'End', startUS: '3000000000', endUS: '3240000000'}],
} as const;
