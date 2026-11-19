/** The screen frames (design/ui-v1 `system-frames.html`). FRAMES.md beside this file is the brief for building a screen on them. */
export {BrowseFrame, browseGridCard, type BrowseFrameProps, type BrowseHeaderProps, type BrowseFiltersProps} from './BrowseFrame';
export {HomeHero, type HomeHeroEntry, type HomeHeroProps} from './HomeHero';
export {TitleFrame, TitleSection, type TitleFrameProps, type TitleCollapse, type TitleAction, type TitlePrimary, type TitleCredit, type TitleKicker, type TitleCover} from './TitleFrame';
export {PanelBlock, PanelHeading, ChoiceChips, type ChoiceChip, AboutTable, CastRow, TrackPicker, NumberedRow, OwnerCard, EpisodeNeighbour, SeasonListRow, TVSeasonList, TVEpisodeShelf, EpisodeBand, type TVSeasonItem, type TVEpisodeItem, type EpisodeBandData, VersionRows, EpisodeDisplay, TitleDetails, PeopleSheet, type PeopleSheetProps, type AboutValue, type CastPerson, type TitleCreditLine, type TitleDetailsProps} from './title-parts';
export {SettingsListFrame, SettingsPageFrame, type SettingsListFrameProps, type SettingsPageFrameProps} from './SettingsFrame';
export {EpisodeList, EpisodePlate, type EpisodeListProps, type EpisodeListItem, type EpisodePlateProps, type EpisodeNeighbourItem, type EpisodeCreditPerson} from './EpisodeList';
export {SheetFrame, type SheetFrameProps, type SheetBarProps} from './SheetFrame';
export {posterGridScroller, titleRowsScroller, type PosterGridItem, type PosterGridScrollerProps, type TitleRowsScrollerProps} from './PosterGridScroller';
export {TitleListRow, type TitleListItem} from './TitleListRow';
export {SignInFrame, type SignInFrameProps} from './SignInFrame';
export {PlayerChromeFrame, type PlayerChromeFrameProps, type PlayerReadouts, type PlayerForm} from './PlayerChromeFrame';
export {AudioPlayerFrame, type AudioPlayerFrameProps, type AudioControl, type AudioOption} from './AudioPlayerFrame';
export {PageStateFrame, type PageStateProps} from './PageStateFrame';
export {type PageHeaderProps} from './PageHeader';
export {Section, SectionCard, StateRow, ArtBox, ArtPlaceholder, type StateRowProps, type SectionStale} from './parts';
// Round 8, the kinds of screen that had no frame.
export {SearchFrame, type SearchFrameProps, type SearchNone} from './SearchFrame';
export {LiveFrame, type LiveFrameProps} from './LiveFrame';
export {SplitFrame, type SplitFrameProps, type FramePane} from './SplitFrame';
