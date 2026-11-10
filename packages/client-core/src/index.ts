/** Everything the apps import from client-core. This file only re-exports. */
export {linkAbortSignals, throwIfAborted} from './abort-lifetime.ts';
export {releaseAll, releaseQuietly, releaseQuietlyAsync, releaseThenRethrow, setCleanupLog} from './cleanup.ts';
export * from './read-preparation.ts';
export * from './server-links.ts';
export * from './file-info-windows.ts';
export * from './server-clock.ts';
export * from './file-info-pages.ts';
export * from './file-info-session.ts';
export {DATABASE_BUSY, WRITER_CAPACITY, WRITER_RETRY_BUDGET_MS, resendableRefusal, retryWriterCapacity, writerRetryDelayMs} from './writer-capacity.ts';
export * from './reading-mark-all.ts';
export {contentEntrySubject} from './presentation/content-entry-subject.ts';
export * from './reading-reread.ts';
export * from './reading-indexed-search.ts';
export * from './reading-offline-refresh.ts';
export * from './reading.ts';
export * from './route-identity.ts';
export * from './clone-portable-dto.ts';
export * from './owner-recovery-contact.ts';
export * from './identity-restrictions.ts';
export * from './identity-devices.ts';
export * from './account-notifications.ts';
export * from './account-capacity.ts';
export * from './owner-claim-client.ts';
export * from './route-connection.ts';
export * from './known-servers.ts';
export * from './portico-servers.ts';
export * from './remote-source-choice.ts';
export * from './audio-effects.ts';
export * from './music-preferences.ts';
export * from './music-signal.ts';
export * from './music-facts.ts';
export * from './music-mixes.ts';
export * from './music-mixes-controller.ts';
export * from './certificates.ts';
export * from './delivery.ts';
export * from './client-profile.ts';
export type {ChannelStartOptions} from './playback-v1/channel-control.ts';
export type {ChannelReference,ChannelProjection} from './playback/linear-protocol.ts';
export type {AudioPlan,AudioBinding,AudioSelectionSnapshot} from './audio-selection.ts';
export * from './outbox.ts';
export type {OutboxKind} from './outbox-routes.ts';
export * from './http.ts';
export * from './viewer-service.ts';
export * from './playback-service.ts';
export {
  CatalogService,
  type CatalogSnapshot,
  type CatalogApi,
} from "./catalog.ts";

export { DeviceAuthorizationService, HttpDeviceAuthorizationApi, type DeviceAuthorization, type DeviceAuthorizationApi, type DeviceAuthorizationSnapshot } from "./device-authorization.ts";

export { HostedGate, hostedGate, type HostedGateSnapshot, type HostedRequestKind } from "./hosted-gate.ts";
export { CredentialSessionService,HttpCredentialApi,type CredentialContext,type CredentialRecord,type CredentialStorage,type CredentialApi,type CredentialSnapshot,type CredentialCoordinator } from "./credentials.ts";
export {LibraryNavigationService,navigationStorageKey,type NavigationScope,type LibraryRoute,type LibraryEntityView,type EntityViewState,type NavigationFocus,type ScrollAnchor,type LibraryNavigationMetadata,type LibraryViewState,type LibraryViewUpdate,type NavigationStorage,type LibraryNavigationSnapshot} from './library-navigation.ts';
export {readListeningSimilar,LibraryContentService,type ContentView,type ContentSurface,type ContentRoute,type ContentScope,type ContentReadContext,type LibraryContentApi,type ContentEntryKind,type ContentHeading,type ContentEntry,type ContentSection,type ContentProjection,type LibraryContentSnapshot} from './library-content.ts';

export * from './hosted-security.ts';
export {DetailService,type DetailScope,type DetailTarget,type DetailApi,type PersonalState,type PersonalOfflineMutation,type PersonalConflict,type DetailAction,type DetailMetadata,type DetailEpisodeInfo,type DetailSongInfo,type DetailBookFileInfo,type DetailItem,type DetailProjection,type PersonalIntent,type DetailError,type DetailSnapshot} from './detail.ts';
export * from "./personal-saved.ts";
export {SearchService,searchPath,parseSearchHistory,type SearchGroup,type SearchSort,type SearchDirection,type SearchGroupStatus,type SearchGroupErrorCode,type SearchQuery,type SearchEntry,type SearchGroupResult,type SearchGroupCapability,type SearchCapabilities,type SearchData,type SearchSnapshot,type SearchHistoryEntry,type SearchHistoryPage} from './search.ts';
export {readSearchGenres, releaseSearchGenres, searchGenresReadMetadata, searchGenresPath, parseSearchGenres, parseSearchGenrePreparing, type SearchGenreArt, type SearchGenre, type SearchGenrePage, type SearchGenreRequest, type SearchGenreReadOptions} from './search-genres.ts';
export * from './facet-windows.ts';
export * from './people.ts';
export {savedListPath,personalHistoryPath,savedListFilters,savedListSorts,historyPeriods,type SavedListFilter,type SavedListSort,type HistoryPeriod,type SavedApi,type SavedScope,type SavedRoute,type SavedActor} from './saved.ts';
export * from './browse.ts';
export * from './browse-buckets.ts';
export * from './browse-whole-selection.ts';
export * from './social-playback.ts';
export * from './group-session.ts';
export * from './remote-playback.ts';
export * from './library-pins.ts';
export * from './subtitles.ts';
export * from './subtitle-originals.ts';
export * from "./lyrics.ts";

export {type MusicPolicy,type MusicPolicyInput,type MusicMatchObservation,type MusicEvidenceDetails,type LocalBookMetadata,musicEvidenceLabel,fingerprintStatusLabel} from "./music-metadata.ts";

export {saveLocalBookPolicy,parseLocalAudioPolicy,type LocalAudioPolicy} from './music-metadata.ts';
export {MetadataRepairService,validateRepair} from "./metadata-repair.ts";
export type {RepairTarget,RepairScope,RepairData,RepairCommand,RepairView,RepairPreview,RepairField,RepairFieldSpec} from "./metadata-repair.ts";
export {validBulkEdit,validBulkListEntry,validateBulkReceipt,bulkConflicts,bulkTargetLimit} from './metadata-bulk.ts';
export type {BulkEdit,BulkTarget,BulkListEdit,BulkFieldEdit,BulkResult,BulkReceipt} from './metadata-bulk.ts';
export * from './prepared-media.ts';

export * from './trickplay.ts';
export {PlayerChaptersService} from './player-chapters.ts';
export type {PlayerChapter,PlayerChaptersData,PlayerChaptersTarget,PlayerChaptersSnapshot} from './player-chapters.ts';
export {PlayerSessionChoices,playerSessionChoices} from './player-session-choices.ts';
export * from './listening.ts';
export * from './preferences.ts';
export * from './preference-effects.ts';
export {parseSegmentMarkers,parseSegmentMarkerSet,canSkipAutomatically,type SegmentMarker,type SegmentMarkerKind,type SegmentMarkerSet} from './segment-markers.ts';
export {activeSegment,skipsAutomatically,skipTarget,followedByScene,closingKind,SKIP_LEAD_SECONDS,UNDO_SECONDS,type SkipMode} from './segment-skip.ts';
export {parseQueuePostPlay,passoutCheckRequired,type QueuePostPlay,type QueueAdvanceMode} from './post-play.ts';
export * from './home.ts';
export * from './play-history.ts';
export * from './recommendations.ts';
export * from './relation-rows.ts';
export * from './notifications.ts';
export * from './notification-scope.ts';
export {detailExtraTypes,type DetailExtra,type DetailExtraType,parseCreditPage,type CreditPage,type DetailCredit} from './detail.ts';

export * from './downloads.ts';
export * from './bulk-jobs.ts';
export * from './container-state.ts';
export * from './download-requests.ts';
export * from './inbox.ts';
export * from './identity-client.ts';
export * from './sse.ts';
export * from './downloads-service.ts';

export * from './client-reports.ts';
export * from './live-handover.ts';

export * from './metadata-attention.ts';
export * from './metadata-settings.ts';

export {parseFreshnessChange, subscribeFreshness, type FreshnessCounters, type FreshnessChange, type CatalogueFailure} from './freshness.ts';
export * from './freshness-hub.ts';
export * from './freshness-actions.ts';
export * from './event-feed.ts';
export * from './home-service.ts';
export * from './home-layout-editor.ts';
export * from './scan-issues.ts';
export * from './playback-controls.ts';
export {sizeClassForWidth, type SizeClass} from './presentation/size-class.ts';

export {readLibrarySummaries,parseLibrarySummary} from './library-summary.ts';
export type {LibrarySummary} from './library-summary.ts';

export {browseServerFilesystem} from './library-management.ts';
export {libraryNavigationIcon} from './presentation/library-navigation.ts';


export {PeopleInvitationsClient,parsePublicInvitation,privatePersonNotes,personIdentity} from './direct-account.ts';
export type {ServerInvitation,ServerShortInvitation,PeopleMember,PublicInvitation} from './direct-account.ts';
export {highestRatingChoices} from './profile-limits.ts';

export * from './account-reset.ts';
export * from './same-tab-return.ts';
export {buildHostedSignInStartURL, parseHostedSignInStartURL} from './hosted-sign-in-start.ts';

export * from './report-context.ts';
export {AppSessionStore, parseAppSessionRecord, type AppSessionRecord, type AppSessionStorage, type AppServerSession, type NewServerConnection, type PorticoAccountScope, type SignInKind} from './app-sessions.ts';
export {ServerSignInProbe, parseServerSignIn, negotiateServer, CLIENT_API, APP_UPDATE_REQUIRED, type ServerAPI, API_LEVEL_MISMATCH, moveOfferDays, signInLabelKey, serverSignInSettingsURL, accountSignInKeys, type ServerSignIn, type ServerSignInSnapshot} from './sign-in.ts';
export * from './account-move.ts';
export {TVConsentApprover, type TVConsentAuthority, type TVConsentTransport} from './tv-consent.ts';

export {videoPictureName, videoAudioFormatName, videoTrackName, videoTrackWords, formatVideoBytes} from './video-track-labels.ts';
export type {VideoTrackWords, VideoTrackNameParts} from './video-track-labels.ts';

export {createReadingHost, connectReadingHost} from './reading-host.ts';
export type {ReadingHostSession, ReadingHostTransport, ReadingGuest, ReadingHostContentsTarget, ReadingHostCrease, ReadingHostSearchAll} from './reading-host.ts';
export {savedViewSummary, type SavedViewSummary, type SavedViewSummaryText} from './presentation/saved-view-summary.ts';
export {readLibrarySettings,saveLibrarySettings,reorderLibraries,saveLibraryFolder,removeLibraryFolder} from './server-admin/libraries.ts';
export {readLibraryInventoryConfig} from './library-inventory.ts';
export {setLibraryAnalysis,setLibraryScanDepth,libraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from './server-admin/library-policy.ts';
export type {LibraryAdminDocument,LibrarySettings} from './administration.ts';
export {readLibrarySonic,saveLibrarySonic,retryLibrarySonic,librarySonicAvailable,type LibrarySonicDocument,type LibrarySonicState} from './server-admin/library-sonic.ts';
export {librarySonicStatus,type LibrarySonicStatusLine} from './server-admin/library-sonic-words.ts';

export {parsePlaybackInfo} from './playback-v1/playback-info.ts';
export type {PlaybackInfo, PlaybackRoute, PlaybackPictureFacts, PlaybackSoundFacts, PlaybackSubtitleFacts, PlaybackMediaFacts, PlaybackReplacement} from './playback-v1/playback-info.ts';

export {playbackInfoProjection, reconciledPlaybackLosses} from './playback-info-projection.ts';
export type {PlaybackInfoWords, PlaybackComparisonProjection, PlaybackInfoRow, PlaybackInfoField} from './playback-info-projection.ts';
export {videoAudioChannelName} from './video-track-labels.ts';

export type {ReadingSavedGroupTarget, ReadingSavedGroupKind, ReadingSavedGroupMembership, ReadingSavedGroupCommand, SavedReadingGroupCard} from './reading-saved-group.ts';
