/** The TV Library module. It owns only the logic of the ten-foot surface a Library route has
 * beyond the shared page: the F-LIB-52 Save view name keyboard's (ruling 37) one-opening store. The panel itself is
 * kit/library/TVSaveViewPanel. Grid, toolbar, filters and paging stay in the shared
 * Library destination and are never duplicated here. */
export {createNativeTVLibraryNamingStore, nativeTVLibraryActions, nativeTVNameEdit, nativeTVNamingFirstKey, nativeTVNamingPanelId} from './naming';
export type {NativeTVLibraryNaming, NativeTVLibraryNamingStore} from './naming';
