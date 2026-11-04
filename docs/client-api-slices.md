# Client API move: exact request sites

Product source: `7ca1f0cef7e49b2ba1e10561c60d04670a2b3b36` (unchanged by the census). After reviewed false-positive fixes and all approved CC-0/CC-2/CC-3 pins: **533 direct sites; 157 approved exception occurrences in 155 ledger entries**.

The census merges before slices. Each slice moves reads and writes across every app surface with unchanged DTO/auth/retry/recovery/cache/freshness behavior; no visual or interaction change. Desktop inherits web; Vega inherits native. Direct account, quick-connect and invitations belong to slice 1; events/native playback to CC-3; saved/personal/downloads/search history to CC-2. CC-0 moves revision-required-fetch.

Reproduce current exact identities, full expressions, source hashes, operation IDs and file:line with `python3 ci/client_api_census.py --report <json>`. Lines below refer to the product snapshot. The checked-in allowlist and approvals ledger, rather than this planning snapshot, own exceptions. Guards include the approved raw/alias/three-argument/chapter-equality shapes; all bind the complete reviewed function/method span. The chapter comparison URL never issues a request.

## slice 1: Connection/auth/setup (101 sites)

| Exact file:line | Kind / callee | Operation IDs / dynamic disposition |
|---|---|---|
| `apps/native/src-next/platform/account-client.ts:21` | request `nativeNetworkFetch` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/platform/account-security.ts:117` | request `new HttpLocalApi(hostedUrl, token, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing job |
| `apps/native/src-next/platform/account-workspace.ts:62` | request `new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/platform/account-workspace.ts:73` | request `request` | dynamic/wrapper: inspect enclosing security |
| `apps/native/src-next/platform/account-workspace.ts:75` | request `request` | servers_list |
| `apps/native/src-next/platform/accounts.ts:181` | request `new HttpLocalApi(hostedUrl, admitted.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing me |
| `apps/native/src-next/platform/portico-sign-in.ts:148` | request `this.api().request` | dynamic/wrapper: inspect enclosing forgot |
| `apps/native/src-next/platform/revision-required-fetch.ts:43` | request `fetcher` | dynamic/wrapper: inspect enclosing revisionRequiredFetch |
| `apps/native/src-next/platform/revision-required-fetch.ts:46` | request `fetcher` | dynamic/wrapper: inspect enclosing revisionRequiredFetch |
| `apps/native/src-next/platform/revision-required-fetch.ts:51` | url `encodedRoutePath` | dynamic/wrapper: inspect enclosing readPath |
| `apps/native/src-next/platform/revision-required-fetch.ts:56` | request `fetcher` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/platform/revision-required-fetch.ts:75` | request `fetcher` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/platform/server-connections.ts:147` | request `nativeRequest` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/platform/server-connections.ts:157` | request `nativeRequest` | dynamic/wrapper: inspect enclosing nativeFetchForIdentity |
| `apps/native/src-next/platform/server-connections.ts:183` | request `nativeRequest` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/platform/server-connections.ts:309` | request `nativeNetworkFetch` | get_connection |
| `apps/native/src-next/platform/server-connections.ts:430` | request `new HttpLocalApi(origin, session.accessToken, nativeNetworkFetch) .request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/platform/server-display.ts:41` | request `api.request` | get_system |
| `apps/native/src-next/platform/server-sign-in.ts:276` | request `new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/platform/server-sign-in.ts:349` | request `this.api.request` | get_direct_account |
| `apps/native/src-next/platform/server-sign-in.ts:479` | request `api.request` | get_system |
| `apps/native/src-next/platform/server-sign-in.ts:544` | request `new HttpLocalApi(hostedUrl, issued.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/platform/session-lifecycle-native.ts:214` | request `restored.api.request` | get_direct_account |
| `apps/native/src-next/platform/session-lifecycle-native.ts:281` | request `api.request` | get_direct_account |
| `apps/native/src-next/platform/session-revocation.ts:55` | request `api.request` | delete_sessions_current |
| `apps/native/src-next/platform/setup-approval.ts:56` | request `new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch) .request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/platform/setup-approval.ts:87` | request `new HttpLocalApi(hostedUrl, session.accessToken, nativeNetworkFetch) .request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/platform/tv-sign-in.ts:38` | request `api.request` | post_quick_connect |
| `apps/native/src-next/platform/tv-sign-in.ts:40` | request `api.request` | post_quick_connect_token |
| `apps/native/src-next/platform/tv-sign-in.ts:43` | request `api.request` | post_quick_connect_cancel |
| `apps/native/src-next/platform/tv-sign-in.ts:55` | request `http.request` | dynamic/wrapper: inspect enclosing create |
| `apps/native/src-next/platform/tv-sign-in.ts:59` | request `http.request` | dynamic/wrapper: inspect enclosing envelope |
| `apps/native/src-next/platform/tv-sign-in.ts:67` | request `http.request` | dynamic/wrapper: inspect enclosing cancel |
| `apps/native/src-next/screens/accounts/AccountRoutes.tsx:146` | request `context.api.requestRaw` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/accounts/AccountRoutes.tsx:185` | request `context.api.request` | get_direct_account |
| `apps/native/src-next/screens/settings-live/DeviceAboutPanel.tsx:20` | request `context.api.request` | get_system |
| `apps/native/src-next/shell/bootstrap-services.ts:186` | request `this.request` | get_direct_account |
| `apps/native/src-next/shell/bootstrap.ts:171` | request `api.request` | get_me |
| `apps/native/src-next/shell/bootstrap.ts:463` | request `readers.request` | get_me |
| `apps/native/src-next/shell/bootstrap.ts:539` | request `readers.request` | get_me |
| `apps/vega/src/bridge/exposure-revocation.ts:15` | request `xhr.open` | dynamic/wrapper: inspect enclosing results |
| `web/src/platform/account-entry.ts:180` | request `request` | dynamic/wrapper: inspect enclosing request |
| `web/src/platform/account-entry.ts:183` | request `api.request` | get_system |
| `web/src/platform/account-entry.ts:231` | request `connected.api.request` | get_system |
| `web/src/platform/account-entry.ts:247` | request `cleanup.request` | delete_sessions_current |
| `web/src/platform/account-entry.ts:329` | request `api.request` | dynamic/wrapper: inspect enclosing preview |
| `web/src/platform/account-entry.ts:344` | request `api.request` | post_access_invitations_accept |
| `web/src/platform/account-entry.ts:364` | request `request` | dynamic/wrapper: inspect enclosing request |
| `web/src/platform/account-entry.ts:630` | request `guarded.request` | get_direct_account |
| `web/src/platform/account-entry.ts:690` | request `api.request` | get_system |
| `web/src/platform/account-entry.ts:804` | request `restored.api.request` | get_system |
| `web/src/platform/account-entry.ts:805` | request `restored.api.request` | get_direct_account |
| `web/src/platform/account-entry.ts:862` | request `api.request` | get_direct_account |
| `web/src/platform/account-security.ts:33` | request `api.send` | dynamic/wrapper: inspect enclosing originalSecurityClient |
| `web/src/platform/account-server-access.ts:25` | request `account.api.send` | dynamic/wrapper: inspect enclosing value |
| `web/src/platform/account-server-directory.ts:18` | request `account.api.send` | servers_list |
| `web/src/platform/account.ts:182` | request `fetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/platform/account.ts:208` | request `this.send` | dynamic/wrapper: inspect enclosing request |
| `web/src/platform/account.ts:231` | request `fetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/platform/account.ts:251` | request `this.request` | dynamic/wrapper: inspect enclosing refresh |
| `web/src/platform/account.ts:254` | request `this.request` | dynamic/wrapper: inspect enclosing revoke |
| `web/src/platform/app-sessions.ts:40` | request `api.request` | get_direct_account |
| `web/src/platform/owner-claim.ts:107` | request `captured.request` | get_networking_claim |
| `web/src/platform/owner-claim.ts:110` | request `captured.request` | post_networking_claim_cancel |
| `web/src/platform/owner-claim.ts:173` | request `captured.request` | get_networking_claim |
| `web/src/platform/owner-claim.ts:180` | request `captured.request` | post_networking_claim_prepare |
| `web/src/platform/owner-claim.ts:241` | request `captured.request` | get_networking_claim |
| `web/src/platform/owner-claim.ts:244` | request `captured.request` | post_networking_claim_cancel |
| `web/src/platform/owner-claim.ts:266` | request `temporary.request` | delete_sessions_current |
| `web/src/platform/owner-claim.ts:301` | request `connected.api.request` | get_networking_claim |
| `web/src/platform/owner-claim.ts:310` | request `connected.api.request` | post_auth_portico_return_exchange |
| `web/src/platform/registered-sign-in.ts:115` | request `temporary.request` | delete_sessions_current |
| `web/src/platform/server-connections.ts:133` | request `globalThis.fetch` | get_connection |
| `web/src/platform/server-connections.ts:154` | request `central.api.send` | dynamic/wrapper: inspect enclosing request |
| `web/src/platform/server-display.ts:39` | request `paired.api.request` | get_system |
| `web/src/screens/account/feedback-controller.ts:52` | request `this.api.request` | dynamic/wrapper: inspect enclosing answer |
| `web/src/screens/accounts/Profiles.route.tsx:52` | request `account.api.send` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/settings/account-profile/security-transport.ts:9` | url `routePath` | post_direct_two_factor_enrol |
| `web/src/screens/settings/account-profile/security-transport.ts:9` | url `routePath` | post_direct_password |
| `web/src/screens/settings/account-profile/security-transport.ts:10` | url `routePath` | post_direct_two_factor_verify |
| `web/src/screens/settings/account-profile/security-transport.ts:10` | url `routePath` | delete_direct_two_factor |
| `web/src/screens/settings/account-profile/security-transport.ts:11` | url `routePath` | post_direct_two_factor_recovery_codes |
| `web/src/screens/settings/account-profile/security-transport.ts:16` | request `original.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/settings/account-profile/source.ts:54` | request `this.api.request` | get_direct_account |
| `web/src/screens/settings/owner-switch.ts:57` | request `temporary.request` | delete_sessions_current |
| `web/src/screens/setup/browser-setup.ts:16` | request `connected.api.request` | get_system |
| `web/src/services/invitation-account.ts:16` | request `account.api.send` | dynamic/wrapper: inspect enclosing request |
| `web/src/services/owner-claim.ts:109` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/owner-claim.ts:175` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/owner-claim.ts:202` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/owner-pending.ts:163` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/setup-claim.ts:79` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/setup-claim.ts:126` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/setup-claim.ts:156` | request `account.api.send` | dynamic/wrapper: inspect enclosing raw |
| `web/src/services/sign-in.ts:64` | request `account.api.send` | dynamic/wrapper: inspect enclosing request |
| `web/src/shell/bootstrap.ts:108` | request `api.request` | get_direct_account |
| `web/src/shell/bootstrap.ts:129` | request `api.request` | get_system |
| `web/src/shell/bootstrap.ts:129` | request `api.request` | get_direct_account |
| `web/src/shell/bootstrap.ts:364` | request `api.request` | get_direct_account |
| `web/src/shell/facts.ts:151` | request `o.api.request` | get_system |
| `web/src/shell/facts.ts:187` | request `o.api.request` | get_direct_account |

## CC-1: Catalogue/browse/search/reading (136 sites)

| Exact file:line | Kind / callee | Operation IDs / dynamic disposition |
|---|---|---|
| `apps/desktop/src/offline.ts:108` | request `this.transport.api.request` | get_download_preparation |
| `apps/desktop/src/offline.ts:180` | request `this.transport.api.request` | list_download_receipt_keys |
| `apps/desktop/src/offline.ts:182` | request `this.transport.api.request` | dynamic/wrapper: inspect enclosing outcomes |
| `apps/desktop/src/offline.ts:226` | request `this.transport.api.request` | dynamic/wrapper: inspect enclosing receipt |
| `apps/desktop/src/offline.ts:236` | request `this.transport.api.request` | list_download_revocations |
| `apps/native/Native/PorticoReadingByteStream.swift:77` | request `bytes` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoSubtitleSelection.m:135` | url `stringWithFormat` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoSubtitleSelection.m:210` | request `requestWithURL` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoSubtitleSelection.m:213` | request `dataTaskWithRequest` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoSubtitleSelection.kt:223` | request `Builder` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoSubtitleSelection.kt:228` | request `newCall` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/src-next/platform/profile-avatars.ts:38` | url `api.mediaUrl` | dynamic/wrapper: inspect enclosing uri |
| `apps/native/src-next/platform/profile-avatars.ts:57` | request `api.requestRaw` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/platform/top-shelf.ts:62` | request `owner.request` | post_topshelf_token |
| `apps/native/src-next/screens/destinations/bulk-media.ts:119` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/destinations/bulk-media.ts:194` | request `this.request` | post_metadata_bulk |
| `apps/native/src-next/screens/destinations/bulk-media.ts:257` | request `this.request` | post_admin_media_delete_preview |
| `apps/native/src-next/screens/destinations/bulk-media.ts:291` | request `this.request` | post_admin_media_delete |
| `apps/native/src-next/screens/destinations/music/model.ts:90` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/destinations/music/model.ts:126` | request `options.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/destinations/music/model.ts:171` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/destinations/music/model.ts:274` | request `request` | dynamic/wrapper: inspect enclosing capabilities |
| `apps/native/src-next/screens/destinations/music/model.ts:280` | request `request` | dynamic/wrapper: inspect enclosing browse |
| `apps/native/src-next/screens/destinations/music/model.ts:288` | request `request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/destinations/music/model.ts:329` | url `encodedRoutePath` | get_items_id_similar |
| `apps/native/src-next/screens/destinations/music/model.ts:383` | request `request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/destinations/music/model.ts:404` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/destinations/music/model.ts:430` | url `encodedRoutePath` | get_artists_id_albums |
| `apps/native/src-next/screens/destinations/music/model.ts:434` | request `request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/destinations/music/model.ts:464` | request `request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/destinations/pins.ts:32` | request `options.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/home/CustomizeRoute.tsx:74` | request `context.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/home/SeeAllRoute.tsx:106` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/home/row-controller.ts:52` | request `this.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/home/row-controller.ts:61` | request `this.request` | dynamic/wrapper: inspect enclosing [raw, row] |
| `apps/native/src-next/screens/home/row-controller.ts:93` | request `this.request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/home/row-controller.ts:108` | request `this.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/home/row-controller.ts:121` | request `this.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/home/row-controller.ts:149` | request `this.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/home/saved-view-summaries.ts:40` | request `this.ports.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/library/filters.ts:73` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/library/filters.ts:103` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/library/model.ts:106` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/library/model.ts:167` | request `this.request` | dynamic/wrapper: inspect enclosing capabilities |
| `apps/native/src-next/screens/library/model.ts:215` | request `this.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/library/save-view.ts:20` | request `options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/notifications/register.tsx:82` | request `context.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/notifications/register.tsx:83` | request `context.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/read/ReadOwnerEditSheet.tsx:75` | request `context.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/read/read-collection-capability.ts:26` | request `options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/read/read-collection-picker.ts:63` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/read/reading-page-opening.ts:51` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/search/genre-browse.ts:43` | request `this.options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/search/genre-browse.ts:64` | request `this.request` | dynamic/wrapper: inspect enclosing capabilities |
| `apps/native/src-next/screens/search/genre-browse.ts:88` | request `this.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/search/loaded-destinations.ts:63` | request `options.api.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/search/loaded-destinations.ts:116` | request `this.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/search/register.tsx:60` | request `context.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/share/NativeContentLinks.tsx:35` | request `context.api.request` | dynamic/wrapper: inspect enclosing read |
| `apps/native/src-next/screens/share/NativeShareLinks.tsx:31` | request `context.api.request` | dynamic/wrapper: inspect enclosing read |
| `apps/native/src-next/screens/share/share-open.ts:39` | url `encodedRoutePath` | get_items_id |
| `apps/native/src-next/shell/bootstrap-services.ts:153` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/shell/bootstrap.ts:274` | request `readers.request` | get_libraries |
| `apps/native/src-next/shell/bootstrap.ts:356` | request `readers.request` | get_libraries |
| `apps/native/src-next/shell/bootstrap.ts:464` | request `readers.request` | get_libraries |
| `apps/native/src-next/shell/bootstrap.ts:601` | url `local.mediaUrl` | dynamic/wrapper: inspect enclosing resolve |
| `apps/native/src-next/shell/bootstrap.ts:652` | request `local.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/shell/listening-song-detail.ts:82` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/shell/music-discover-navigation.tsx:30` | request `context.api.request` | get_libraries |
| `apps/vega/src/bridge/transport.ts:75` | request `xhr.open` | dynamic/wrapper: inspect enclosing start |
| `apps/vega/src/debug.tsx:34` | request `request.open` | dynamic/wrapper: inspect enclosing send |
| `web/src/screens/browse/Library.route.tsx:111` | request `original.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/Library.route.tsx:163` | request `life.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/browse/Library.route.tsx:201` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/browse/Library.route.tsx:253` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/browse/Library.route.tsx:336` | request `resources.life.api.request` | dynamic/wrapper: inspect enclosing ContextLibrary |
| `web/src/screens/browse/Library.route.tsx:427` | request `resources.life.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/LibraryDeleteSurface.tsx:31` | request `options.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/controller.ts:107` | request `this.api.request` | dynamic/wrapper: inspect enclosing caps |
| `web/src/screens/browse/controller.ts:245` | request `this.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/browse/controller.ts:266` | request `this.api.request` | dynamic/wrapper: inspect enclosing page |
| `web/src/screens/browse/controller.ts:278` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/browse/play-selection.ts:134` | request `originalApi.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/selection-action-transfers.ts:111` | request `this.options.api.request` | get_download_preparation |
| `web/src/screens/browse/selection-action-transfers.ts:123` | request `this.options.api.request` | create_download_grant |
| `web/src/screens/browse/selection-delete-query.ts:27` | request `ports.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/browse/selection-delete.ts:58` | request `ports.api.request` | post_admin_media_delete_preview |
| `web/src/screens/browse/selection-delete.ts:94` | request `ports.api.request` | post_admin_media_delete |
| `web/src/screens/browse/selection-editor.ts:71` | request `api.request` | get_items_id |
| `web/src/screens/browse/selection-editor.ts:76` | request `api.request` | get_metadata_kind_id |
| `web/src/screens/browse/selection-editor.ts:127` | request `api.request` | post_metadata_bulk |
| `web/src/screens/browse/selection-picker.ts:32` | request `ports.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/selection-picker.ts:58` | request `this.api.request` | post_home_destination_picker |
| `web/src/screens/browse/selection.tsx:283` | request `pickers.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/browse/useBrowseLease.ts:32` | request `original.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/detail/Detail.tsx:306` | request `binding.context.api.request` | dynamic/wrapper: inspect enclosing refresh |
| `web/src/screens/detail/audiobooks/bindings.ts:85` | request `life.api.request` | dynamic/wrapper: inspect enclosing capabilities |
| `web/src/screens/detail/bindings.ts:113` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/detail/bindings.ts:192` | url `encodedRoutePath` | get_items_id_credits |
| `web/src/screens/detail/bindings.ts:193` | request `life.api.request` | dynamic/wrapper: inspect enclosing page |
| `web/src/screens/detail/context.ts:76` | request `context.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/detail/context.ts:85` | request `context.api.requestRaw` | dynamic/wrapper: inspect enclosing requestRaw |
| `web/src/screens/detail/context.ts:88` | request `context.api.requestRaw` | dynamic/wrapper: inspect enclosing requestRaw |
| `web/src/screens/detail/context.ts:91` | request `context.api.requestRaw` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/detail/genre-binding.ts:9` | request `api.request` | dynamic/wrapper: inspect enclosing capabilities |
| `web/src/screens/detail/show-bindings.ts:47` | request `life.api.request` | dynamic/wrapper: inspect enclosing capabilities |
| `web/src/screens/detail/show-bindings.ts:160` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/device/local-device-opening.ts:15` | url `routePath` | post_quick_connect_decision |
| `web/src/screens/device/local-device-opening.ts:15` | url `routePath` | post_quick_connect_review |
| `web/src/screens/device/local-device-opening.ts:63` | request `request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/live/bindings.ts:17` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/live/bindings.ts:53` | url `routePath` | get_guide |
| `web/src/screens/live/recordings-controller.ts:98` | request `sourceApi.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/live/series-rules-bindings.ts:63` | request `life.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/read/ReadBulkMetadata.tsx:42` | request `held.life.api.request` | post_metadata_bulk |
| `web/src/screens/read/ReadItemEntry.tsx:226` | request `context.api.request` | get_metadata_kind_id |
| `web/src/screens/read/ReadMetadata.tsx:20` | request `life.api.request` | get_metadata_kind_id |
| `web/src/screens/read/bindings.ts:72` | request `context.api.farewell` | delete_reading_sessions_sessionId |
| `web/src/screens/read/bindings.ts:115` | request `context.api.farewell` | delete_reading_sessions_sessionId |
| `web/src/screens/read/facets.ts:32` | request `request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/read/fonts.ts:11` | request `fetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/read/useReadingMarkAll.ts:35` | request `held.life.api.request` | dynamic/wrapper: inspect enclosing useReadingMarkAll |
| `web/src/screens/read/useReadingMarkAll.ts:53` | request `held.life.api.request` | dynamic/wrapper: inspect enclosing useReadingMarkAll |
| `web/src/screens/read/useReadingMarkAll.ts:63` | request `held.life.api.request` | dynamic/wrapper: inspect enclosing rawCurrent |
| `web/src/screens/search/bindings.ts:128` | request `life.api.request` | get_guide |
| `web/src/shell/SelectionSurfaces.tsx:158` | request `metadata.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/shell/artwork.ts:94` | request `api.routeFetch` | dynamic/wrapper: inspect enclosing fetchImage |
| `web/src/shell/artwork.ts:128` | url `encodedRoutePath` | get_library_channels_id_logo |
| `web/src/shell/artwork.ts:142` | url `encodedRoutePath` | get_admin_access_members_id_profiles_profile_avatar |
| `web/src/shell/bootstrap.ts:125` | request `// One slow or failed library directory never stops the shell: it is unavailable, never empty, // and the shell's directory interest reads it again. Authority answers still stop sign-in. api.request` | get_libraries |
| `web/src/shell/bootstrap.ts:435` | request `api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/shell/entry-delete.ts:58` | request `ports.api.request` | post_admin_media_delete_preview |
| `web/src/shell/entry-delete.ts:94` | request `ports.api.request` | post_admin_media_delete |
| `web/src/shell/entry-picker.ts:19` | request `ports.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/shell/facts.ts:133` | request `o.api.request` | get_libraries |
| `web/src/shell/title-menu-host.ts:169` | request `life.api.request` | dynamic/wrapper: inspect enclosing /** Run a row. Resolves once the effect it started has finished (for tests); the menu is already closed. */
  choose |

## CC-2: Saved/personal/downloads (23 sites)

| Exact file:line | Kind / callee | Operation IDs / dynamic disposition |
|---|---|---|
| `apps/desktop/src/downloads-host.ts:57` | url `input.api.mediaUrl` | dynamic/wrapper: inspect enclosing resolvePublishedPath |
| `apps/native/platform/offline-downloads.ts:1118` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/platform/offline-downloads.ts:1260` | request `api.request` | dynamic/wrapper: inspect enclosing remove |
| `apps/native/platform/offline-downloads.ts:1347` | request `api.request` | dynamic/wrapper: inspect enclosing outcomes |
| `apps/native/platform/offline-downloads.ts:1386` | request `api.request` | dynamic/wrapper: inspect enclosing outcomes |
| `apps/native/platform/offline-downloads.ts:1437` | request `api.request` | dynamic/wrapper: inspect enclosing revalidateReceipts |
| `apps/native/platform/offline-downloads.ts:1505` | request `api.request` | dynamic/wrapper: inspect enclosing keys |
| `apps/native/platform/offline-downloads.ts:1763` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/platform/offline-downloads.ts:1813` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/platform/offline-downloads.ts:1940` | request `api.request` | dynamic/wrapper: inspect enclosing receipt |
| `apps/native/src-next/screens/destinations/saved/model.ts:136` | request `this.options.api.request` | post_personal_history_actions |
| `apps/native/src-next/screens/downloads/ReadingDownloadSheet.tsx:77` | request `context.api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/downloads/ReadingDownloadSheet.tsx:100` | request `context.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/platform/browser-download.ts:38` | request `api.request` | get_items_id |
| `web/src/platform/browser-download.ts:41` | request `api.request` | get_download_options |
| `web/src/platform/browser-download.ts:70` | request `api.request` | create_download_preparations |
| `web/src/platform/browser-download.ts:85` | request `api.request` | get_download_preparation |
| `web/src/platform/browser-download.ts:91` | request `api.request` | create_download_grant |
| `web/src/platform/browser-download.ts:95` | url `api.mediaUrl` | dynamic/wrapper: inspect enclosing href |
| `web/src/screens/saved/runtime.ts:56` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/saved/view-capabilities.ts:39` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/watch/collection-next.ts:29` | url `encodedRoutePath` | get_collections_id_items |
| `web/src/screens/watch/collection-next.ts:31` | request `api.request` | dynamic/wrapper: inspect enclosing raw |

## CC-3: Playback/group/receiver (63 sites)

| Exact file:line | Kind / callee | Operation IDs / dynamic disposition |
|---|---|---|
| `apps/native/Native/PorticoPlayer.m:378` | request `requestWithURL` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoPlayer.m:387` | request `requestWithURL` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoServerTrust.m:337` | request `requestWithURL` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/Native/PorticoServerTrust.m:340` | request `dataTaskWithRequest` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoNetwork.kt:191` | request `Builder` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoNetwork.kt:193` | request `newCall` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt:190` | request `request` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt:200` | request `request` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt:242` | request `request` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt:262` | request `request` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoTransport.kt:145` | request `Builder` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoTransport.kt:196` | request `newCall` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/android/app/src/main/java/tv/getportico/portico/native/PorticoTransport.kt:210` | request `openStream` | dynamic/wrapper: inspect enclosing <native> |
| `apps/native/src-next/platform/play-on/cast.ts:86` | request `this.request` | dynamic/wrapper: inspect enclosing item |
| `apps/native/src-next/platform/play-on/cast.ts:116` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/platform/play-on/cast.ts:308` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/platform/play-on/cast.ts:382` | request `this.request` | dynamic/wrapper: inspect enclosing item |
| `apps/native/src-next/platform/play-on/cast.ts:399` | request `this.request` | dynamic/wrapper: inspect enclosing item |
| `apps/native/src-next/platform/playback/capacity-retry.ts:35` | url `routePath` | start_playback_session |
| `apps/native/src-next/platform/playback/capacity-retry.ts:35` | url `routePath` | create_queue |
| `apps/native/src-next/platform/playback/native.ios.tsx:77` | request `options.api.request` | dynamic/wrapper: inspect enclosing policy |
| `apps/native/src-next/platform/playback/native.tsx:86` | request `options.api.request` | dynamic/wrapper: inspect enclosing policy |
| `apps/native/src-next/platform/playback/preparing-owner.ts:47` | url `encodedRoutePath` | get_playback_session |
| `apps/native/src-next/platform/playback/preparing-owner.ts:48` | url `encodedRoutePath` | stop_playback_session |
| `apps/native/src-next/platform/playback/preparing-owner.ts:56` | request `fetcher` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/platform/playback/runtime.ts:505` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:572` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:611` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:612` | request `this.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/platform/playback/runtime.ts:752` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:878` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:939` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:985` | request `this.request` | get_capabilities |
| `apps/native/src-next/platform/playback/runtime.ts:1277` | request `this.request` | get_items_id |
| `apps/native/src-next/platform/playback/runtime.ts:1401` | request `this.request` | dynamic/wrapper: inspect enclosing applyOptions |
| `apps/native/src-next/platform/playback/runtime.ts:1454` | request `this.request` | dynamic/wrapper: inspect enclosing readForcedImage |
| `apps/native/src-next/screens/settings-live/server/troubleshooting/playback-problems-source.ts:20` | request `this.context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/together-playback.ts:95` | request `context.api.request` | get_items_id |
| `apps/native/src-next/screens/watch/disc-titles.ts:69` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/watch/disc-titles.ts:80` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/watch/disc-titles.ts:92` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/watch/live-actions.ts:81` | request `opening.source.api.request` | get_guide |
| `apps/native/src-next/screens/watch/live-guide.ts:115` | request `api.request` | get_guide |
| `apps/native/src-next/screens/watch/title-actions.tsx:152` | request `context.api.request` | dynamic/wrapper: inspect enclosing refreshMetadata |
| `web/src/platform/event-feed.ts:50` | request `api.routeFetch` | get_events |
| `web/src/platform/playback/capacity-retry.ts:36` | url `routePath` | start_playback_session |
| `web/src/platform/playback/capacity-retry.ts:36` | url `routePath` | create_queue |
| `web/src/platform/playback/runtime.ts:145` | request `api.request` | get_capabilities |
| `web/src/platform/playback/runtime.ts:647` | request `this.source.api.request` | get_items_id |
| `web/src/platform/playback/runtime.ts:762` | request `this.source.api.request` | get_items_id |
| `web/src/platform/playback/runtime.ts:1646` | url `encodedRoutePath` | get_items_id |
| `web/src/platform/playback/runtime.ts:1650` | request `this.source.api.request` | dynamic/wrapper: inspect enclosing readItemBounded |
| `web/src/platform/playback/start-owner.ts:57` | url `encodedRoutePath` | get_playback_session |
| `web/src/platform/playback/trickplay.ts:38` | request `source.api.request` | get_items_id_analysis |
| `web/src/platform/playback/trickplay.ts:45` | request `source.api.request` | dynamic/wrapper: inspect enclosing view |
| `web/src/platform/playback/web-playback.ts:799` | request `this.host.farewell` | dynamic/wrapper: inspect enclosing // ── The page going away ─────────────────────────────────────────────────

  /**
   * The page is being hidden. `final`: it is going for good (a close, a reload, a navigation),
   * so the session is stopped with its position in a request that outlives the page; the stream
   * slot and any converter are released at once instead of when the lease runs out. Otherwise
   * (a hidden tab, a page kept for Back) the newest position is reported and playback stays.
   */
  pageHidden |
| `web/src/platform/playback/web-playback.ts:806` | request `this.host.farewell` | dynamic/wrapper: inspect enclosing // ── The page going away ─────────────────────────────────────────────────

  /**
   * The page is being hidden. `final`: it is going for good (a close, a reload, a navigation),
   * so the session is stopped with its position in a request that outlives the page; the stream
   * slot and any converter are released at once instead of when the lease runs out. Otherwise
   * (a hidden tab, a page kept for Back) the newest position is reported and playback stays.
   */
  pageHidden |
| `web/src/screens/together/bindings.ts:29` | request `context.api.routeFetch` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/together/bindings.ts:39` | request `api.request` | get_items_id |
| `web/src/screens/watch/cast-metadata.ts:14` | request `api.request` | get_items_id |
| `web/src/screens/watch/cast-metadata.ts:24` | request `api.request` | get_items_id |
| `web/src/screens/watch/receivers.ts:143` | request `api.request` | post_quick_connect_review |
| `web/src/screens/watch/receivers.ts:151` | request `api.request` | post_quick_connect_decision |

## CC-4: Owner/settings/admin (210 sites)

| Exact file:line | Kind / callee | Operation IDs / dynamic disposition |
|---|---|---|
| `apps/native/src-next/screens/settings-live/LiveRoute.tsx:17` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/NativeAccountSecurityPanels.tsx:250` | request `scope.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/NativeHostedSignInPanel.tsx:74` | request `new HttpLocalApi(hostedUrl, value.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing state |
| `apps/native/src-next/screens/settings-live/NativeServerOfflineNoticeRow.tsx:54` | request `new HttpLocalApi(hostedUrl, live.accessToken, nativeNetworkFetch).request` | dynamic/wrapper: inspect enclosing run |
| `apps/native/src-next/screens/settings-live/NativeTVReceiver.tsx:65` | request `context.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/NativeTVReceiver.tsx:77` | request `context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/RecordingsScreen.tsx:164` | request `ports.context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/SettingsRoute.tsx:38` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/SettingsRoute.tsx:38` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/SettingsScreen.tsx:149` | request `ports.context.api.request` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/SettingsScreen.tsx:228` | request `ports.context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/SettingsScreen.tsx:332` | request `ports.context.api.request` | get_admin_updates |
| `apps/native/src-next/screens/settings-live/TogetherScreen.tsx:218` | request `ports.context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/TogetherScreen.tsx:241` | request `ports.context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/live-controller.ts:44` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/live-controller.ts:147` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:43` | request `this.api.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:49` | request `this.request` | dynamic/wrapper: inspect enclosing load |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:81` | request `this.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:87` | request `this.request` | dynamic/wrapper: inspect enclosing raw |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:93` | request `this.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:100` | request `this.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:108` | request `this.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:112` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:113` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:115` | request `this.request` | dynamic/wrapper: inspect enclosing put |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:129` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:136` | request `this.request` | dynamic/wrapper: inspect enclosing page |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:149` | request `this.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:153` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:154` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:155` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:160` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:162` | request `this.api.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:177` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:179` | request `this.request` | dynamic/wrapper: inspect enclosing [owners, grants] |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:179` | request `this.request` | dynamic/wrapper: inspect enclosing [owners, grants] |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:183` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:185` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:203` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:230` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:236` | request `this.request` | dynamic/wrapper: inspect enclosing guide |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:240` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:241` | request `this.request` | dynamic/wrapper: inspect enclosing <module> |
| `apps/native/src-next/screens/settings-live/live-owner-controller.ts:252` | request `this.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/owner-dashboard/reader.ts:42` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/owner-dashboard/reader.ts:46` | request `context.api.request` | dynamic/wrapper: inspect enclosing controls |
| `apps/native/src-next/screens/settings-live/owner-libraries/AddLibraryPage.tsx:37` | request `api.request` | dynamic/wrapper: inspect enclosing AddLibraryPage |
| `apps/native/src-next/screens/settings-live/owner-libraries/IntroCreditsReview.tsx:23` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/owner-libraries/IntroCreditsReview.tsx:49` | request `context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/owner-libraries/IntroCreditsReview.tsx:104` | request `context.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/owner-libraries/OwnerLibrariesPanel.tsx:70` | request `originalApi.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/owner-libraries/OwnerLibrariesPanel.tsx:70` | request `originalApi.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/owner-libraries/controller.ts:147` | request `this.api.request` | dynamic/wrapper: inspect enclosing [document, , , metadata] |
| `apps/native/src-next/screens/settings-live/owner-libraries/controller.ts:302` | request `this.api.request` | post_libraries_id_lyrics_fetch |
| `apps/native/src-next/screens/settings-live/owner-libraries/controller.ts:433` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/owner-libraries/controller.ts:450` | request `this.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/owner-libraries/controller.ts:459` | request `this.api.request` | dynamic/wrapper: inspect enclosing metadata |
| `apps/native/src-next/screens/settings-live/server/BroadcastSheet.tsx:66` | request `api.request` | dynamic/wrapper: inspect enclosing send |
| `apps/native/src-next/screens/settings-live/server/GeneralPage.tsx:57` | request `api.request` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/server/GeneralPage.tsx:91` | request `api.request` | dynamic/wrapper: inspect enclosing loadUpdates |
| `apps/native/src-next/screens/settings-live/server/HistoryPage.tsx:38` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/NotificationDestinations.tsx:20` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/RemoteAccessPage.tsx:51` | request `api.request` | get_networking_certificate, post_networking_certificate_config, post_networking_certificate_retry |
| `apps/native/src-next/screens/settings-live/server/StreamingPage.tsx:35` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/StreamingPage.tsx:61` | request `owned.api.request` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/server/StreamingPage.tsx:129` | request `owned.api.request` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/server/TVLiveDefaults.tsx:31` | request `ports.context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/TVLiveDefaults.tsx:98` | request `ports.context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/TVRemoteAccessPanel.tsx:32` | request `api.request` | get_networking_certificate |
| `apps/native/src-next/screens/settings-live/server/TVRemoteAccessPanel.tsx:37` | request `api.request` | get_networking_remote |
| `apps/native/src-next/screens/settings-live/server/TVRemoteAccessPanel.tsx:38` | request `api.request` | get_connectivity_status |
| `apps/native/src-next/screens/settings-live/server/TVRemoteAccessPanel.tsx:41` | request `api.request` | get_admin_security_summary |
| `apps/native/src-next/screens/settings-live/server/TVServerReadOnlyPanel.tsx:49` | request `api.request` | dynamic/wrapper: inspect enclosing read |
| `apps/native/src-next/screens/settings-live/server/TVServerReadOnlyPanel.tsx:59` | url `routePath` | get_sign_in_settings |
| `apps/native/src-next/screens/settings-live/server/TVServerReadOnlyPanel.tsx:98` | url `routePath` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/server/TVServerReadOnlyPanel.tsx:99` | url `routePath` | get_metadata_settings |
| `apps/native/src-next/screens/settings-live/server/TVServerSettingsPage.tsx:53` | request `api.request` | get_settings_configuration |
| `apps/native/src-next/screens/settings-live/server/TVServerSettingsPage.tsx:62` | request `api.request` | get_admin_transcode_capacity |
| `apps/native/src-next/screens/settings-live/server/channels/logo-artwork.ts:11` | url `encodedRoutePath` | get_library_channels_id_logo |
| `apps/native/src-next/screens/settings-live/server/channels/logo-upload.ts:21` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/channels/logo-upload.ts:32` | url `encodedRoutePath` | post_admin_library_channels_id_logo |
| `apps/native/src-next/screens/settings-live/server/channels/logo-upload.ts:36` | request `context.api.routeFetch` | dynamic/wrapper: inspect enclosing response |
| `apps/native/src-next/screens/settings-live/server/channels/model.ts:17` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/integrations/ApiKeys.tsx:22` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/integrations/AppPasswords.tsx:23` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/integrations/Webhooks.tsx:22` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/member-profiles-source.ts:34` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/member-profiles-source.ts:38` | request `context.api.requestRaw` | dynamic/wrapper: inspect enclosing requestRaw |
| `apps/native/src-next/screens/settings-live/server/metadata-controller.ts:19` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/server/people-source.ts:45` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/people-source.ts:125` | request `this.api.request` | get_libraries |
| `apps/native/src-next/screens/settings-live/server/people-source.ts:144` | request `this.api.request` | get_admin_access_devices |
| `apps/native/src-next/screens/settings-live/server/storage/BackupFolder.tsx:28` | request `api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/BackupsTab.tsx:34` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/BackupsTab.tsx:73` | request `group.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/DownloadsUsage.tsx:21` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/EmptyCache.tsx:18` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/MissingTitlesTab.tsx:29` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/storage/forms.ts:45` | request `originalApi.requestConsole` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/server/storage/forms.ts:51` | request `originalApi.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/server/storage/space-source.ts:32` | request `this.context.api.request` | dynamic/wrapper: inspect enclosing read |
| `apps/native/src-next/screens/settings-live/server/storage/space-source.ts:36` | url `routePath` | get_data_storage_usage |
| `apps/native/src-next/screens/settings-live/server/storage/space-source.ts:36` | url `routePath` | get_admin_storage_usage |
| `apps/native/src-next/screens/settings-live/server/troubleshooting/Feedback.tsx:24` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/troubleshooting/Messages.tsx:24` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/troubleshooting/Reports.tsx:21` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/server/troubleshooting/Support.tsx:36` | request `context.api.request` | dynamic/wrapper: inspect enclosing value |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:19` | url `routePath` | get_admin_access_devices |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:20` | url `routePath` | get_admin_access_invitations |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:21` | url `routePath` | get_admin_access_members |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:36` | url `routePath` | dynamic/wrapper: inspect enclosing path |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:37` | request `context.api.request` | dynamic/wrapper: inspect enclosing peoplePage |
| `apps/native/src-next/screens/settings-live/server/tv-server-reader.ts:44` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/settings-controller.ts:50` | request `api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings-live/settings-panels.tsx:124` | request `context.api.request` | dynamic/wrapper: inspect enclosing controls |
| `apps/native/src-next/screens/settings-live/together-media.ts:55` | request `this.api.request` | get_items_id |
| `apps/native/src-next/screens/settings-live/use-settings-request-scope.ts:15` | request `context.api.request` | dynamic/wrapper: inspect enclosing request |
| `apps/native/src-next/screens/settings/remote-sources/controller.ts:60` | request `originalApi.request` | dynamic/wrapper: inspect enclosing result |
| `apps/native/src-next/screens/settings/remote-sources/controller.ts:149` | request `originalApi.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/screens/dashboard/bindings.ts:94` | request `options.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/dashboard/bindings.ts:100` | request `options.api.requestRaw` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/dashboard/bindings.ts:107` | request `queries.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/dashboard/bindings.ts:120` | url `routePath` | get_networking_certificate, post_networking_certificate_config, post_networking_certificate_retry |
| `web/src/screens/dashboard/bindings.ts:121` | request `options.api.request` | dynamic/wrapper: inspect enclosing requestCertificate |
| `web/src/screens/dashboard/bindings.ts:126` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/dashboard/bindings.ts:143` | url `routePath` | list_admin_sessions |
| `web/src/screens/dashboard/entry.phone.tsx:36` | url `encodedRoutePath` | get_items_id_art_kind |
| `web/src/screens/live/owner/OwnerSourcesPage.tsx:55` | request `options.api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/screens/live/owner/discovery-controller.ts:11` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/live/owner/library-channel-admission.ts:21` | url `encodedRoutePath` | get_items_id_art_kind |
| `web/src/screens/live/owner/library-channel-upload.ts:18` | url `encodedRoutePath` | post_admin_library_channels_id_logo |
| `web/src/screens/live/owner/library-channel-upload.ts:23` | request `context.api.routeFetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/live/owner/remote-source-controller.ts:33` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/live/owner/remote-source-controller.ts:52` | request `this.api.request` | dynamic/wrapper: inspect enclosing receipt |
| `web/src/screens/live/owner/source-controller.ts:31` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/live/owner/source-controller.ts:47` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/live/owner/source-controller.ts:65` | request `this.api.request` | dynamic/wrapper: inspect enclosing receipt |
| `web/src/screens/live/owner/upload-source-controller.ts:36` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/live/owner/upload-source-controller.ts:46` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/metadata/NeedsAttention.tsx:26` | request `api.request` | dynamic/wrapper: inspect enclosing service |
| `web/src/screens/settings/LinkTvDialog.tsx:52` | request `api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/settings/SendAppLog.tsx:44` | request `capture.api.request` | dynamic/wrapper: inspect enclosing SendAppLog |
| `web/src/screens/settings/SendAppLog.tsx:64` | request `capture.api.requestRaw` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/settings/Settings.route.tsx:199` | request `originalApi.requestRaw` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/Settings.route.tsx:202` | request `originalApi.requestConsole` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/Settings.route.tsx:217` | request `originalApi.routeFetch` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/Settings.route.tsx:252` | request `api.routeFetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/settings/Settings.route.tsx:391` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/Settings.route.tsx:406` | request `api.routeFetch` | dynamic/wrapper: inspect enclosing response |
| `web/src/screens/settings/Settings.route.tsx:440` | url `encodedRoutePath` | get_items_id_art_kind |
| `web/src/screens/settings/Settings.route.tsx:477` | request `api.routeFetch` | dynamic/wrapper: inspect enclosing open |
| `web/src/screens/settings/Settings.route.tsx:535` | url `encodedRoutePath` | get_items_id_art_kind |
| `web/src/screens/settings/controller.ts:166` | request `options.api.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/screens/settings/libraries/LibrariesPanel.tsx:57` | request `api.request` | dynamic/wrapper: inspect enclosing LibrariesPanel |
| `web/src/screens/settings/libraries/MetadataSource.tsx:19` | request `api.request` | dynamic/wrapper: inspect enclosing MetadataSource |
| `web/src/screens/settings/libraries/MetadataSource.tsx:32` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/libraries/lyrics-fetch.ts:30` | request `this.options.api.request` | post_libraries_id_lyrics_fetch |
| `web/src/screens/settings/libraries/skipped-files.ts:17` | url `encodedRoutePath` | get_scan_issues |
| `web/src/screens/settings/libraries/skipped-files.ts:18` | request `api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/library-source.ts:144` | request `this.api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/metadata/MetadataSettingsPage.tsx:63` | request `api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/screens/settings/people-devices-api.ts:12` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/people-devices-api.ts:17` | request `captured.request` | dynamic/wrapper: inspect enclosing list |
| `web/src/screens/settings/people-member-profiles-api.ts:16` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/secure-address.ts:68` | request `api.request` | get_connectivity_status |
| `web/src/screens/settings/secure-address.ts:69` | request `api.request` | get_networking_remote |
| `web/src/screens/settings/server-livetv/channel-map-controller.ts:26` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/channel-map-controller.ts:48` | request `this.api.request` | dynamic/wrapper: inspect enclosing rawPage |
| `web/src/screens/settings/server-livetv/channel-map-controller.ts:50` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/channel-map-controller.ts:59` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/permissions-controller.ts:26` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/permissions-controller.ts:32` | request `this.api.request` | dynamic/wrapper: inspect enclosing grants |
| `web/src/screens/settings/server-livetv/recording/permissions-controller.ts:62` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/permissions-controller.ts:67` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/rules-controller.ts:44` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/rules-controller.ts:81` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/rules-controller.ts:99` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/rules-controller.ts:113` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/recording/tuner-controller.ts:10` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/source-choices-controller.ts:17` | request `this.api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/screens/settings/server-livetv/source-settings-controller.ts:60` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/source-settings-controller.ts:79` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-livetv/source-settings-controller.ts:106` | request `this.api.request` | dynamic/wrapper: inspect enclosing fresh |
| `web/src/screens/settings/server-livetv/source-settings-controller.ts:119` | request `this.api.request` | dynamic/wrapper: inspect enclosing saved |
| `web/src/screens/settings/server-livetv/source-settings-controller.ts:144` | request `this.api.request` | dynamic/wrapper: inspect enclosing raw |
| `web/src/screens/settings/server-remote/certificate-form-source.ts:38` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/server-remote/certificate-form-source.ts:43` | request `request` | get_networking_certificate, post_networking_certificate_config, post_networking_certificate_retry |
| `web/src/screens/settings/server-remote/remote-form-source.ts:47` | request `api.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/server/GeneralTools.tsx:42` | request `api.request` | post_admin_notifications_broadcast |
| `web/src/screens/settings/server/storage-source.ts:69` | request `this.transport.request` | get_admin_storage_usage |
| `web/src/screens/settings/server/storage-source.ts:70` | request `this.transport.request` | get_data_storage_usage |
| `web/src/screens/settings/settings-request.ts:17` | request `original.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/settings-request.ts:17` | request `original.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/screens/settings/settings-request.ts:34` | request `original.request` | dynamic/wrapper: inspect enclosing request |
| `web/src/services/OriginalAccountSecurityWorkspace.tsx:23` | request `super.send` | dynamic/wrapper: inspect enclosing send |
| `web/src/services/OriginalAccountSecurityWorkspace.tsx:35` | request `api.send` | dynamic/wrapper: inspect enclosing client |
| `web/src/services/ServersPanel.tsx:61` | request `api.request` | dynamic/wrapper: inspect enclosing leave |
| `web/src/services/auth-flow.ts:161` | request `this.account.client.request` | dynamic/wrapper: inspect enclosing cancelProvider |
| `web/src/services/auth-flow.ts:215` | request `this.account.client.request` | dynamic/wrapper: inspect enclosing /** One field names the account: its username or the email on it, never both. */
  recover |
| `web/src/services/link-effects.ts:9` | request `account.client.request` | dynamic/wrapper: inspect enclosing resetPassword |
| `web/src/services/link-effects.ts:36` | request `account.client.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/services/link-effects.ts:69` | request `account.client.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/services/link-effects.ts:82` | request `account.client.request` | dynamic/wrapper: inspect enclosing value |
| `web/src/services/session.ts:10` | request `account.api.send` | dynamic/wrapper: inspect enclosing client |
| `web/src/services/workspace-api.ts:12` | request `account.api.send` | dynamic/wrapper: inspect enclosing request |
| `web/src/shell/entry-metadata.tsx:41` | request `value.ports.api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/shell/entry-metadata.tsx:57` | request `api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/shell/entry-metadata.tsx:86` | request `value.ports.api.request` | dynamic/wrapper: inspect enclosing result |
| `web/src/shell/entry-metadata.tsx:109` | request `api.request` | dynamic/wrapper: inspect enclosing request |
