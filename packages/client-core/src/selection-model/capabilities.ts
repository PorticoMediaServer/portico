import type {
  SelectionActionRule, SelectionActionRules, SelectionCapabilities, SelectionEnvironment,
  BrowseSelectionScope, SelectionIdentity, SelectionItem, SelectionKind, SelectionSummary,
} from './types.ts';

export const selectionKinds: readonly SelectionKind[] = Object.freeze([
  'movie', 'show', 'season', 'episode', 'artist', 'album', 'song', 'book',
  'audiobook_file', 'chapter', 'collection', 'playlist', 'category', 'author', 'book_series', 'disc', 'extra',
  'channel', 'person',
]);
export const selectionCapabilityNames = Object.freeze([
  'play', 'queue', 'playlist', 'myList', 'watched', 'collection', 'favorite',
  'download', 'edit', 'refresh', 'delete',
] as const);

export function selectionKindHasWatched(kind: SelectionKind): boolean {
  return kind === 'movie' || kind === 'show' || kind === 'season' || kind === 'episode' || kind === 'extra';
}

export function selectionKindHasBulkFields(kind: SelectionKind): boolean {
  return ['movie', 'show', 'episode', 'song', 'album', 'artist', 'book', 'audiobook_file'].includes(kind);
}

/** DESIGN and Hard rule 5 apply to every result, including People and Live TV. */
export function selectableSelectionKind(kind: SelectionKind, _surface: 'search' | 'catalogue'): boolean {
  return selectionKinds.includes(kind);
}

/** Non-catalogue commands must be backed by the real guide/person service. */
export function selectionKindAllowsCapability(kind: SelectionKind, name: keyof SelectionCapabilities): boolean {
  if (!selectionKinds.includes(kind)) return false;
  if (kind === 'person') return false;
  if (kind === 'channel') return name === 'play' || name === 'favorite';
  return name !== 'watched' || selectionKindHasWatched(kind);
}

/** Missing facts grant nothing. Music's Played state never becomes Watched. */
export function selectionCapabilities(
  kind: SelectionKind,
  offered: Partial<SelectionCapabilities>,
): SelectionCapabilities {
  return Object.freeze(Object.fromEntries(selectionCapabilityNames.map(name => [
    name, selectionKinds.includes(kind) && offered[name] === true && selectionKindAllowsCapability(kind, name),
  ])) as Record<keyof SelectionCapabilities, boolean>);
}

export function selectionKey(scope: {
  serverId: string; authority: string; accountId: string; profileId: string; viewId: string; privacyFence: string;
}, item: SelectionIdentity): string {
  return JSON.stringify([
    scope.serverId, scope.authority, scope.accountId, scope.profileId, scope.viewId, scope.privacyFence,
    ...(item.kind === 'channel' ? [item.kind, item.sourceId, item.channelId]
      : item.kind === 'person' ? [item.kind, item.id] : [item.kind, item.libraryId, item.id]),
  ]);
}

/** No invented privacy fence: Browse identity stays in its own context domain. */
export function browseSelectionKey(scope: BrowseSelectionScope, item: SelectionIdentity): string {
  return JSON.stringify(['browse', scope.serverId, scope.authority, scope.accountId, scope.profileId, scope.viewId,
    ...(item.kind === 'channel' ? [item.kind, item.sourceId, item.channelId]
      : item.kind === 'person' ? [item.kind, item.id] : [item.kind, item.libraryId, item.id])]);
}

/** Cost is proportional to explicit picks, never the size of the catalogue. */
export function summarizeSelection(items: readonly SelectionItem[]): SelectionSummary {
  const types = new Map<SelectionKind, number>();
  let libraryId: string | undefined;
  let multiple = false;
  let unbound = false;
  const every = Object.fromEntries(selectionCapabilityNames.map(name => [name, items.length > 0])) as Record<keyof SelectionCapabilities, boolean>;
  for (const item of items) {
    types.set(item.kind, (types.get(item.kind) ?? 0) + 1);
    if (item.kind === 'channel' || item.kind === 'person') unbound = true;
    else if (libraryId === undefined) libraryId = item.libraryId;
    else if (libraryId !== item.libraryId) multiple = true;
    for (const name of selectionCapabilityNames) {
      every[name] &&= item.capabilities[name] === true && selectionKindAllowsCapability(item.kind, name);
    }
  }
  return Object.freeze({
    count: items.length,
    types: Object.freeze(selectionKinds.flatMap(kind => {
      const count = types.get(kind);
      return count === undefined ? [] : [Object.freeze({kind, count})];
    })),
    library: unbound && libraryId !== undefined ? Object.freeze({kind: 'unknown' as const})
      : multiple ? Object.freeze({kind: 'multiple' as const})
      : libraryId === undefined ? Object.freeze({kind: 'none' as const})
      : Object.freeze({kind: 'single' as const, libraryId}),
    every: Object.freeze(every),
  });
}

const hidden: SelectionActionRule = Object.freeze({state: 'hidden'});
const ready: SelectionActionRule = Object.freeze({state: 'ready'});
const explain = (reason: 'mixed-types' | 'no-bulk-fields' | 'delete-limit'): SelectionActionRule => Object.freeze({state: 'explain', reason});
const disabled = (reason: 'not-permitted' | 'one-library-required' | 'unsupported-sort' | 'preview-limit'): SelectionActionRule => Object.freeze({state: 'disabled', reason});

/** Product availability only. The existing server still authorises and previews every write. */
export function selectionActions(summary: SelectionSummary, environment: SelectionEnvironment): SelectionActionRules {
  const active = summary.count > 0 && summary.types.length > 0;
  const allowed = (name: keyof SelectionCapabilities): SelectionActionRule => active && summary.every[name]
    && summary.types.every(type => selectionKindAllowsCapability(type.kind, name)) ? ready : hidden;
  const catalogueOnly = summary.types.every(type => type.kind !== 'channel' && type.kind !== 'person');
  // A guide Play starts one channel. No existing queue accepts a channel set.
  const playableSet = !summary.types.some(type => type.kind === 'channel') || summary.count === 1;
  // DESIGN's every-platform curation rule overrides the TV exclusion in select's
  // lower-priority notes. Server settings and File info manage different commands.
  const ownerTools = active && catalogueOnly;
  let edit = hidden;
  if (ownerTools && (environment.isOwner || environment.canEditMetadata)) {
    edit = !summary.every.edit ? disabled('not-permitted')
      : summary.types.length !== 1 ? explain('mixed-types')
      : !selectionKindHasBulkFields(summary.types[0].kind) ? explain('no-bulk-fields') : ready;
  }
  let remove = hidden;
  if (ownerTools && environment.isOwner) {
    remove = summary.library.kind !== 'single' ? disabled('one-library-required')
      : !summary.every.delete ? disabled('not-permitted')
      // The action opens the real dialog; it must explain the cap, never chunk a delete.
      : summary.count > 200 ? explain('delete-limit') : ready;
  }
  const restrict = (rule: SelectionActionRule, reason: 'unsupported-sort' | 'preview-limit' | undefined) =>
    rule.state === 'ready' && reason ? disabled(reason) : rule;
  const watched = active && summary.every.watched && summary.types.every(type => selectionKindHasWatched(type.kind)) ? ready : hidden;
  return Object.freeze({
    play: restrict(playableSet ? allowed('play') : hidden, summary.unavailableActions?.play),
    'play-next': restrict(allowed('queue'), summary.unavailableActions?.queue),
    'play-later': restrict(allowed('queue'), summary.unavailableActions?.queue),
    playlist: restrict(allowed('playlist'), summary.unavailableActions?.playlist),
    'my-list': allowed('myList'),
    'mark-watched': watched,
    'mark-unwatched': watched,
    collection: allowed('collection'),
    favorite: allowed('favorite'),
    'remove-my-list': allowed('myList'),
    edit: restrict(edit, summary.unavailableActions?.metadataPreview),
    download: environment.platform === 'phone-app' ? allowed('download') : hidden,
    refresh: ownerTools && environment.isOwner ? allowed('refresh') : hidden,
    delete: restrict(remove, summary.unavailableActions?.delete),
  });
}

/** The fourth phone/episode slot is Watched; My List remains available in More. */
export function selectionFourthAction(summary: SelectionSummary): 'mark-watched' | 'my-list' {
  return summary.count > 0 && summary.types.length === 1 && summary.types[0].kind === 'episode' && summary.every.watched
    ? 'mark-watched' : 'my-list';
}
