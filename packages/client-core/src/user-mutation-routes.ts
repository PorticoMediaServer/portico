import {apiRoot, routes} from '../../contracts/src/routes.ts';
/** Reviewed P2-51 cohort. Paths and methods always come from the generated registry. */
export const userMutationRouteIds = [
  'put_admin_backup_settings',
  'change_console_settings',
  'put_admin_dvr_settings',
  'put_admin_libraries_order',
  'patch_admin_libraries_id',
  'put_admin_libraries_id_scan_policy',
  'put_library_settings',
  'put_admin_libraries_id_shows_show_settings',
  'put_admin_libraries_id_sonic_analysis',
  'set_live_source_configuration',
  'set_live_settings',
  'patch_admin_logs_settings',
  'put_admin_maintenance_settings',
  'put_metadata_settings',
  'patch_admin_notifications_settings',
  'post_channels_preferences',
  'set_container_personal_state',
  'undo_container_personal_state',
  'put_download_settings',
  'put_items_personal_state_batch',
  'put_items_id_personal_state',
  'put_items_itemId_reading_favorite',
  'put_items_itemId_reading_mark',
  'put_items_itemId_reading_my_list',
  'put_libraries_id_metadata_agent',
  'put_libraries_id_metadata_screen',
  'put_libraries_id_strm_analysis_policy',
  'put_reading_group_my_list',
  'post_libraries_libraryId_reading_mark_all',
  'post_library_channels_preferences',
  'put_listening_preferences',
  'post_networking_remote_config',
  'stop_playback_session',
  'stop_account_playback',
  'post_playlists',
  'delete_playlists_id',
  'patch_playlists_id',
  'post_playlists_id_entries',
  'delete_playlists_id_entries_entryId',
  'reorder_playlist_window',
  'delete_playlists_id_shares',
  'put_playlists_id_shares',
  'patch_preferences',
  'delete_reading_saved_group_reference',
  'put_saved_pins_order',
  'put_saved_pins_kind_id',
  'post_saved_resources',
  'delete_saved_resources_id',
  'patch_saved_resources_id',
  'put_saved_resources_id_entries',
  'reorder_saved_collection',
  'delete_saved_resources_id_shares',
  'put_saved_resources_id_shares',
  'put_storage_source_advanced',
  'put_storage_source_program',
  'put_storage_source_read_profile',
] as const;
const reviewed = new Set<string>(userMutationRouteIds);
export const userMutationRoutes = routes.filter(route => reviewed.has(route.id));
if (userMutationRoutes.length !== userMutationRouteIds.length) throw new Error('Missing generated user-mutation route.');
export function userMutationRoute(path: string, method: string) {
 if (!path.startsWith(apiRoot) || /[#\\]/.test(path)) return undefined;
 const segments = path.split('?')[0].split('/');
 return userMutationRoutes.find(route => route.method === method && (() => {
  const pattern = route.path.split('/');
  return pattern.length === segments.length && pattern.every((part, i) => {const open=part.indexOf('{'),close=part.indexOf('}');if(open<0)return part===segments[i];const prefix=part.slice(0,open),suffix=part.slice(close+1),value=segments[i];return value.startsWith(prefix)&&value.endsWith(suffix)&&value.length>prefix.length+suffix.length&&!/%(?:2f|5c)/i.test(value);});
 })());
}
