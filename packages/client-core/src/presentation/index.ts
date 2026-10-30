/**
 * Shared presentation layer (X-04, X-05, X-14): pure, platform-free helpers
 * that decide what a person sees. Web imports `@core/presentation/index.ts`;
 * Apple imports `packages/client-core/src/presentation/index`.
 */
export {shapeFor, shapeAspect, personShape, type CardShape, type ShapeContext, type ShapeFamily} from './card-shape.ts';
export {presentError, errorMessage, type ErrorContext, type ErrorOperation, type ErrorCategory, type ErrorAction, type PresentedError, type PresentOptions} from './errors.ts';
export {preferenceGroupTitle, preferenceLabel, preferenceValueLabel, managedElsewhere, deviceNoun, type CopyPlatform} from './preference-copy.ts';
export {viewerScope, iconFor, captionFor, progressFor, formatDuration, formatClock, formatBytes, friendlyHost, sentence} from './content.ts';
export {compatContentApi, sanitizeContentProjection, knownContentViews, type ContentRequestApi} from './content-compat.ts';
export {canRefreshMetadata, refreshMetadataPath, entryActions, entryActionList, type EntryAction, type EntryActionGroup, type EntryActionGroupId, type EntryActionId, type EntryCapabilities} from './entry-actions.ts';
export {detailActions, type DetailActions, type DetailPrimary, type DetailQuick, type DetailViewer} from './detail-actions.ts';
export {profileActions, type ProfileAction, type ProfileActionId, type ProfileRef, type ProfileViewer} from './profile-actions.ts';
export {localeChoices, type RegionChoice} from './region-choices.ts';
export {playOnDestinations, webDestinationSupport, type DestinationCapabilities, type DestinationPlatform, type DestinationRow, type Destinations, type NearbyPlayer} from './destinations.ts';
export {serviceI18n, serviceProblem, serviceText, setServiceI18n} from './service-text.ts';
export {serverTextLabel, type ServerLabel} from './server-text.ts';
export {isBitmapSubtitleFormat, isBitmapResource, isTextResource, textTrackForLanguage, sourceFactsOf, sourceIs4kOrHdr, bitmapWarningNeeded, ownerPlaybackSwitches, type BitmapTrackLike, type OwnerPlaybackSwitches, type SourceFacts} from './bitmap-subtitles.ts';
export {formatBehindSec, isBehindLive, behindLiveSec, programmeRangeLabel} from './live-label.ts';
export {nowNextProgrammes, miniGuideNeighbors, sortChannelsByNumber, type MiniGuideChannel, type MiniGuideProgramme, type MiniGuideRow} from './mini-guide.ts';
export {dedupQualityRungs, convertQualityRungs, qualityNetworkFooter, hasQualityFooter, type QualityRungLike, type QualitySourceLike, type DeliveryPolicyLike, type QualityNetwork, type QualityFooter} from './quality-model.ts';
export {noteCastMeta, castMeta, type CastMeta} from './cast-meta.ts';
export {toolbarModel, nextToolbarSort, browseNameId, sortDirectionKind, type SortDirectionKind, fieldSelectionSummary, rangeSelectionLabel, decadePresets, toolbarGroupOf, filterControlOf, filterValuesField, rangePresets, activeRangePreset, browseValueLabel, appliedFilterLabel, genreFilter, orderFilterValues, type ToolbarModel, type ToolbarGroup, type ToolbarGroupId, type ToolbarPredicate, type FilterControl, type RangePreset} from './browse-toolbar.ts';
export {relatedRowsForPage, dedupeNames, showPeople, showCreditLines, activeYearsLabel, titleTintHue, seasonProgress, nextUpPin, networkName, originalTitleLine, SHOW_SETTING_ROWS, type ShowPeople} from './title-page.ts';
export {languageKey, sameLanguage, trackName} from './language.ts';
export {titleFileLines, resolutionLabel, codecLabel, dynamicRangeLabel, channelsLabel, formatBitRate, versionNames, mediaVersionNames, subtitleTrackNames, type TitleFileLines, type VersionFacts, type NamedSubtitleTrack, type NamedMediaVersion} from './title-files.ts';
export {settingsStructure, findSettingsPage, settingsPageId, settingsRowId, searchSettings, readComposite, compositePatch, registryComposite, serverFormsOf, serverRowShown, serverChoices, settingsScopeLabel, PLAYER_ENGINES, type PlayerEngineSetting, type SettingsStructure, type SettingsHeading, type SettingsHeadingId, type SettingsPage, type SettingsSection, type SettingsRow, type ServerSettingRow, type ServerControl, type SettingsTab, type SettingsControl, type SettingsChoice, type SettingsContext, type SettingsCapabilities, type SettingsPlatform, type SettingsScope, type SettingsMatch, type CompositeId, type SettingsActionId, type SettingsCustomId} from './settings-structure.ts';
export {MAX_LANGUAGES, COMMON_LANGUAGES, languageName, availableLanguages, addLanguage, removeLanguage, moveLanguage} from './language-list.ts';
export * from './library-tabs.ts';
export * from './card-caption.ts';
export * from './home-hero.ts';
export * from './search-model.ts';
export * from './playback-info.ts';
export * from './manual-order.ts';
export * from './browse-groups.ts';
export * from './licences.ts';
export * as kitModel from './kit-model/index.ts';
export * from './home-spotlight.ts';
export {savedViewSummary, type SavedViewSummary, type SavedViewSummaryText} from './saved-view-summary.ts';
export {orderToolbarPredicates} from './browse-toolbar.ts';
