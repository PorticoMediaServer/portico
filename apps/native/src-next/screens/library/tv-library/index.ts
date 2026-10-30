/** The TV Library module. It owns only the ten-foot surface a Library route has
 * beyond the shared page: the F-LIB-52 Save view name keyboard (ruling 37) and
 * its one-opening store. Grid, toolbar, filters and paging stay in the shared
 * Library destination and are never duplicated here. */
export {createNativeTVLibraryNamingStore, nativeTVLibraryActions, nativeTVNameEdit, nativeTVNamingFirstKey, nativeTVNamingPanelId} from './naming';
export type {NativeTVLibraryNaming, NativeTVLibraryNamingStore} from './naming';
export {NativeTVLibrarySaveViewPanel, NativeTVLibrarySurface} from './TVLibraryScreen';
