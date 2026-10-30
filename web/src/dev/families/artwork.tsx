import {referenceArtworkColors} from '../../kit/artwork/dev-colors';
import {useEffect, useMemo, useState, type ComponentType, type ReactNode} from 'react';
import {defaultI18n, formatMessage} from '@i18n';
import {artworkFixtures} from '../../../../packages/i18n/src/catalog/en-US/next/dev/artwork-fixtures';
import {defineGalleryFamily} from '../gallery-registry';
import {AmbientArt, Artwork, ArtistCircle, ChannelMark, ColourBackdrop, CoverMosaic, GeneratedArt, GeneratedCover, PersonCircle, ProfileFace, ProfilePicker, createArtworkRuntime, type AmbientArtSource, type AmbientArtTreatment, type ArtworkPalette, type ArtworkSource, type ProfilePickerProfile} from '../../kit/artwork';
import styles from '../../kit/artwork/gallery.module.css';
import twentyOne from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/21.jpg?url';
import ram from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/random-access-memories.jpg?url';
import hit from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/hit-me-hard-and-soft.jpg?url';
import damn from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/damn.jpg?url';
import afterHours from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/after-hours.jpg?url';
import abbey from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/abbey-road-2019-mix.jpg?url';
import rumours from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/rumours.jpg?url';
import taylor from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/albums/1989-taylor-s-version-deluxe.jpg?url';
import severance from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/tv/severance--bd.jpg?url';
import jujutsu from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/anime/jujutsu-kaisen.jpg?url';
import spirited from '../../../../../../design-ui-v1/design/ui-v1/assets/bd-spirited.jpg?url';
import martian from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/books/the-martian-unabridged.jpg?url';
import harry from '../../../../../../design-ui-v1/design/ui-v1/assets/pool/books/harry-potter-and-the-philosopher-s-stone.jpg?url';
import referenceLogo from '../../../../../../design-ui-v1/design/ui-v1/assets/wordmark.svg?url';
import nonImage from '../../kit/fonts/OFL.txt?url';

const t = defaultI18n.t;
const fixture = (id: keyof typeof artworkFixtures) => formatMessage(artworkFixtures[id]);
const {palette, gold, teal} = referenceArtworkColors;
type ArtworkEvidence = Readonly<{foreground?: ArtworkSource; ambient?: AmbientArtSource; ambients?: readonly AmbientArtSource[]; palette?: ArtworkPalette; logo?: ArtworkSource; failedLogo?: ArtworkSource; lifecycleSource?: ArtworkSource | null; lifecycleVisible?: boolean; lifecycleDurationMs?: number | null; foregroundTransform?: string}>;
declare global { var __porticoWebArtworkEvidence: ArtworkEvidence | undefined; var __porticoWebArtworkCallbacks: {event: string; at: number; key?: string}[] | undefined; }
function recordCallback(event: string, source?: ArtworkSource) { (globalThis.__porticoWebArtworkCallbacks ??= []).push({event, at: performance.now(), ...(source ? {key: source.key} : {})}); }
/** Dev-only injection of real authority-bound handles; never credentials or fabricated ambient metadata. */
function useArtworkEvidence() {
  const [evidence, setEvidence] = useState(globalThis.__porticoWebArtworkEvidence);
  useEffect(() => {
    const update = () => setEvidence(globalThis.__porticoWebArtworkEvidence ? {...globalThis.__porticoWebArtworkEvidence} : undefined);
    window.addEventListener('portico-artwork-evidence', update);
    return () => window.removeEventListener('portico-artwork-evidence', update);
  }, []);
  return evidence;
}
function useReferenceArt() {
  const runtime = useMemo(() => createArtworkRuntime({scopeKey: 'dev-only-private-design-references',
    load: async ({url}, signal) => {
      const response = await fetch(url, {signal, credentials: 'omit', redirect: 'error'});
      if (!response.ok) throw new Error('Reference image request failed.');
      const blob = await response.blob();
      const src = URL.createObjectURL(blob), image = new Image(); image.src = src;
      try { await image.decode(); if (signal.aborted) throw new DOMException('Aborted', 'AbortError'); }
      catch (error) { URL.revokeObjectURL(src); throw error; }
      return {src, bytes: blob.size, width: image.naturalWidth, height: image.naturalHeight};
    }, classify: () => ({retryable: false}), release: image => URL.revokeObjectURL(image.src),
  }), []);
  useEffect(() => () => runtime.dispose(), [runtime]);
  const sources = useMemo(() => ({twentyOne, ram, hit, damn, afterHours, abbey, rumours, taylor, severance, jujutsu, spirited, martian, harry, referenceLogo, nonImage}), []);
  return useMemo(() => Object.fromEntries(Object.entries(sources).map(([name, url]) => [name, runtime.source({url, cacheKey: url})])) as Record<keyof typeof sources, ReturnType<typeof runtime.source>>, [runtime, sources]);
}
function Stage({children, className = ''}: {children: ReactNode; className?: string}) { return <div className={`${styles.stage} ${className}`}>{children}</div>; }
function ambient(treatment: AmbientArtTreatment, foreground = false, tvFrame = false): ComponentType {
  return function AmbientCase() { const refs = useReferenceArt(), evidence = useArtworkEvidence(); return <Stage className={treatment === 'poster' ? styles.fallback : ''}><AmbientArt treatment={treatment} foregroundFrameStyle={tvFrame ? {width: 1500, height: 900} : undefined} foregroundStyle={evidence?.foregroundTransform ? {transform: evidence.foregroundTransform} : undefined} onReady={source => recordCallback('ambient:onReady', source)} onError={source => recordCallback('ambient:onError', source)} onStatusChange={status => recordCallback(`ambient:${status}`)} palette={evidence ? evidence.palette : palette} source={evidence?.ambient} sources={evidence?.ambients} foreground={foreground ? evidence?.foreground ?? (treatment === 'poster' ? refs.jujutsu : treatment === 'phone' ? refs.spirited : refs.severance) : undefined} /></Stage>; };
}
function boundedAmbient(treatment: AmbientArtTreatment, stage: string): ComponentType {
  return function BoundedAmbientCase() { const evidence = useArtworkEvidence(); return <Stage className={stage}><AmbientArt treatment={treatment} source={evidence?.ambient} visible={evidence?.lifecycleVisible ?? true} onReady={source => recordCallback('ambient:onReady', source)} onError={source => recordCallback('ambient:onError', source)} onStatusChange={status => recordCallback(`ambient:${status}`)} /></Stage>; };
}
function colour(shape: 'side' | 'round' | 'top', fill = false): ComponentType { return () => <Stage><ColourBackdrop shape={shape} fill={fill} className={fill ? styles.stateFull : undefined} /></Stage>; }
type CoverCase = 'large' | 'grid' | 'phone' | 'phone-book' | 'small' | 'long' | 'no-author' | 'longest';
const coverCases = {
  large: {title: 'timeMachine', author: 'wells', glyph: 'T', font: 17.1, tint: gold, width: 300, height: 450},
  grid: {title: 'dracula', author: 'stoker', glyph: 'D', font: 15.5, tint: referenceArtworkColors.red, width: 190, height: 285},
  phone: {title: 'allSystems', author: 'martha', glyph: 'A', font: 15.5, tint: referenceArtworkColors.green, width: 108, height: 162},
  'phone-book': {title: 'timeMachine', author: 'wells', glyph: 'T', font: 16.9, tint: gold, width: 168, height: 252},
  small: {title: 'duneMessiah', author: 'herbert', glyph: 'D', font: 15.5, tint: referenceArtworkColors.brown, width: 36, height: 54},
  long: {title: 'war', author: 'wells', glyph: 'W', font: 12, tint: teal, width: 190, height: 285},
  'no-author': {title: 'jane', glyph: 'J', font: 15.5, tint: referenceArtworkColors.slate, width: 190, height: 285},
  longest: {title: 'metamorphosis', author: 'kafka', glyph: 'M', font: 8.6, tint: referenceArtworkColors.red, width: 190, height: 285},
} as const;
function cover(size: CoverCase): ComponentType {
  return () => { const item = coverCases[size]; return <Stage className={styles.cover}><GeneratedCover title={fixture(`next.artwork.fixture.${item.title}`)} author={'author' in item ? fixture(`next.artwork.fixture.${item.author}`) : undefined} tint={item.tint} glyph={item.glyph} fontScale={item.font} titleClassName={size === 'small' ? styles.compactCoverTitle : undefined} /></Stage>; };
}
function artist(count: number, tv = false): ComponentType { return function ArtistCase() { const refs = useReferenceArt(); return <Stage><ArtistCircle covers={(tv ? count === 2 ? [refs.taylor, refs.twentyOne] : [refs.twentyOne, refs.taylor, refs.abbey, refs.afterHours] : [refs.afterHours, refs.abbey, refs.rumours, refs.taylor]).slice(0, count)} alt={t('next.artwork.case.artist')} size={tv ? 320 : 172} treatment={tv ? 'tv' : 'artist'} /></Stage>; }; }
function person(covers: boolean, initials = covers ? 'JR' : 'MF', size = covers ? 172 : 112): ComponentType { return function PersonCase() { const refs = useReferenceArt(); return <Stage><PersonCircle covers={covers ? [refs.harry] : []} initials={initials} initialsSize={size === 112 ? 34 : undefined} alt={t('next.artwork.case.person')} size={size} /></Stage>; }; }
function mosaic(count: number): ComponentType { return function MosaicCase() { const refs = useReferenceArt(); return <Stage><CoverMosaic className={styles.mosaic} covers={[refs.afterHours, refs.ram, refs.hit, refs.damn].slice(0, count)} alt={t('next.artwork.case.mosaic')} /></Stage>; }; }
function channel(mode: 'monogram' | 'logo' | 'error'): ComponentType { return function ChannelCase() { const refs = useReferenceArt(), evidence = useArtworkEvidence(); return <Stage><ChannelMark mark={{sourceId: 'reference-lineup', id: 'northwind-news-24', text: 'N24', seed: 'reference-lineup:northwind-news-24'}} source={mode === 'error' ? evidence?.failedLogo ?? refs.nonImage : mode === 'logo' ? evidence?.logo ?? refs.referenceLogo : undefined} alt={t('next.artwork.case.channel')} /></Stage>; }; }
const profiles: readonly ProfilePickerProfile[] = [
  {id: 'demo', name: fixture('next.artwork.fixture.demo'), eligible: true, art: 'blue', primary: true},
  {id: 'alex', name: fixture('next.artwork.fixture.alex'), eligible: true, art: 'violet', pinRequired: true},
  {id: 'kids', name: fixture('next.artwork.fixture.kids'), eligible: true, art: 'mint'},
  {id: 'sam', name: fixture('next.artwork.fixture.sam'), eligible: false, art: 'coral', unavailableReason: 'server_restriction'},
  {id: 'jules', name: fixture('next.artwork.fixture.jules'), eligible: false, art: 'gold', unavailableReason: 'membership_required'},
];
function picker(layout: 'desktop' | 'phone' | 'tv'): ComponentType {
  return function PickerCase() {
    const [outcome, setOutcome] = useState('');
    return <Stage className={`${styles.profileStage} ${layout === 'phone' ? styles.profilePhone : layout === 'tv' ? styles.profileTV : ''}`}>
      <ColourBackdrop shape={layout === 'phone' ? 'top' : 'round'} />
      <h1>{t('next.artwork.who')}</h1>{layout === 'phone' && <p>{fixture('next.artwork.fixture.server')}</p>}
      <ProfilePicker profiles={profiles} getProfileTint={profile => referenceArtworkColors.authProfileTints[profile.id]} layout={layout} autoFocusId={layout === 'phone' ? undefined : 'alex'} onFocusChange={id => recordCallback(`profile:focus:${id}`)} onSelect={profile => { recordCallback(`profile:select:${profile.id}`); setOutcome(profile.name); }} onAdd={() => { recordCallback('profile:add'); setOutcome(t('next.artwork.addProfile')); }} onManage={() => { recordCallback('profile:manage'); setOutcome(t('next.artwork.manageProfiles')); }} />
      <output className={styles.outcome}>{outcome}</output>
    </Stage>;
  };
}
function Faces() { return <Stage><div className={styles.row}>{(['blue', 'violet', 'mint', 'coral', 'gold', 'slate', 'rose', 'sky'] as const).map(art => <ProfileFace key={art} initial="D" art={art} size={64} locked={art === 'violet'} selected={art === 'blue'} />)}</div></Stage>; }
function Lifecycle() {
  const refs = useReferenceArt(), evidence = useArtworkEvidence(), [which, setWhich] = useState<'ready' | 'error' | 'empty'>('empty');
  return <Stage className={styles.lifecycle}><div className={styles.artworkBox}><Artwork source={evidence?.lifecycleSource === undefined ? which === 'ready' ? refs.jujutsu : which === 'error' ? refs.nonImage : undefined : evidence.lifecycleSource} visible={evidence?.lifecycleVisible ?? true} arrivalDurationMs={evidence?.lifecycleDurationMs === null ? undefined : evidence?.lifecycleDurationMs ?? 240} onStatusChange={status => recordCallback(status)} onReady={source => recordCallback('onReady', source)} onError={source => recordCallback('onError', source)} alt={t('next.artwork.case.lifecycle')} fallback={<span className={styles.fallbackLabel}>{t('next.artwork.noArtwork')}</span>} /></div>
    <div className={styles.lifecycleControls}><button type="button" className={styles.button} onClick={() => setWhich('ready')}>{fixture('next.artwork.fixture.showArtwork')}</button><button type="button" className={styles.button} onClick={() => setWhich('error')}>{t('next.artwork.case.lifecycle')}</button></div>
  </Stage>;
}
const cases = [
  {id: 'ambient-hero', labelKey: 'next.artwork.case.ambient', width: 1440, height: 720, render: ambient('hero', true)},
  {id: 'ambient-tv-frame', labelKey: 'next.artwork.case.ambient', width: 1920, height: 1080, render: ambient('hero', true, true)},
  {id: 'ambient-title', labelKey: 'next.artwork.case.ambient', width: 1440, height: 660, render: ambient('title', true)},
  {id: 'ambient-phone', labelKey: 'next.artwork.case.ambient', width: 390, height: 560, render: ambient('phone', true)},
  {id: 'ambient-poster', labelKey: 'next.artwork.case.ambient', width: 858, height: 236, render: ambient('poster', true)},
  {id: 'ambient-empty', labelKey: 'next.artwork.case.ambient', width: 1440, height: 720, render: () => <Stage><AmbientArt /></Stage>},
  {id: 'ambient-palette', labelKey: 'next.artwork.case.ambient', width: 1440, height: 900, render: ambient('wash')},
  {id: 'ambient-library', labelKey: 'next.artwork.case.ambient', width: 1440, height: 620, render: ambient('library')},
  {id: 'ambient-artist', labelKey: 'next.artwork.case.ambient', width: 1440, height: 640, render: ambient('artist')},
  {id: 'ambient-cover', labelKey: 'next.artwork.case.ambient', width: 340, height: 340, render: ambient('cover', true)},
  {id: 'ambient-resume', labelKey: 'next.artwork.case.ambient', width: 456, height: 180, render: boundedAmbient('resume', styles.boundedResume)},
  {id: 'ambient-read-resume', labelKey: 'next.artwork.case.ambient', width: 440, height: 204, render: boundedAmbient('resume', styles.boundedResume)},
  {id: 'ambient-resume-phone', labelKey: 'next.artwork.case.ambient', width: 296, height: 150, render: boundedAmbient('resume-phone', styles.boundedResume)},
  {id: 'ambient-library-row', labelKey: 'next.artwork.case.ambient', width: 358, height: 108, render: boundedAmbient('library-row', styles.boundedLibrary)},
  {id: 'ambient-collection-tile', labelKey: 'next.artwork.case.ambient', width: 422, height: 237, render: boundedAmbient('collection-tile', styles.boundedCollection)},
  {id: 'ambient-session-audio', labelKey: 'next.artwork.case.ambient', width: 176, height: 99, render: boundedAmbient('session-audio', styles.boundedSession)},
  {id: 'colour-side', labelKey: 'next.artwork.case.colour', width: 1440, height: 900, render: colour('side')},
  {id: 'colour-round', labelKey: 'next.artwork.case.colour', width: 1440, height: 900, render: colour('round')},
  {id: 'colour-top', labelKey: 'next.artwork.case.colour', width: 390, height: 844, render: colour('top')},
  {id: 'colour-full', labelKey: 'next.artwork.case.colour', width: 390, height: 844, render: colour('top', true)},
  {id: 'generated-art', labelKey: 'next.artwork.case.generated', width: 858, height: 236, render: () => <Stage className={styles.fallback}><GeneratedArt glyph="L" /></Stage>},
  ...(Object.keys(coverCases) as CoverCase[]).map(size => ({id: `generated-cover-${size}`, labelKey: 'next.artwork.case.cover' as const, width: coverCases[size].width, height: coverCases[size].height, render: cover(size)})),
  ...([1, 2, 3, 4] as const).map(count => ({id: `artist-${count}`, labelKey: 'next.artwork.case.artist' as const, width: 172, height: 172, render: artist(count)})),
  ...([1, 2, 3, 4] as const).map(count => ({id: `artist-tv-${count}`, labelKey: 'next.artwork.case.artist' as const, width: 320, height: 320, render: artist(count, true)})),
  {id: 'person-plain', labelKey: 'next.artwork.case.person', width: 112, height: 112, render: person(false)},
  {id: 'person-bare-detail', labelKey: 'next.artwork.case.person', width: 96, height: 96, render: person(false, 'TC', 96)},
  {id: 'person-bare-author', labelKey: 'next.artwork.case.person', width: 112, height: 112, render: person(false, 'FH', 112)},
  {id: 'person-covers', labelKey: 'next.artwork.case.person', width: 172, height: 172, render: person(true)},
  ...([1, 2, 3, 4] as const).map(count => ({id: `mosaic-${count}`, labelKey: 'next.artwork.case.mosaic' as const, width: 190, height: 190, render: mosaic(count)})),
  {id: 'channel-monogram', labelKey: 'next.artwork.case.channel', width: 34, height: 34, render: channel('monogram')},
  {id: 'channel-logo', labelKey: 'next.artwork.case.channel', width: 34, height: 34, render: channel('logo')},
  {id: 'channel-logo-error', labelKey: 'next.artwork.case.channel', width: 34, height: 34, render: channel('error')},
  {id: 'profile-pin-face', labelKey: 'next.artwork.case.profile', width: 148, height: 148, render: () => <Stage><ProfileFace initial="A" art="violet" size={148} locked /></Stage>},
  {id: 'profile-faces', labelKey: 'next.artwork.case.profile', width: 760, height: 112, render: Faces},
  {id: 'profile-picker-desktop', labelKey: 'next.artwork.case.profile', width: 1440, height: 900, render: picker('desktop')},
  {id: 'profile-picker-phone', labelKey: 'next.artwork.case.profile', width: 390, height: 844, render: picker('phone')},
  {id: 'profile-picker-tv', labelKey: 'next.artwork.case.profile', width: 1920, height: 1080, render: picker('tv')},
  {id: 'artwork-lifecycle', labelKey: 'next.artwork.case.lifecycle', width: 390, height: 420, render: Lifecycle},
] as const;
export default defineGalleryFamily({id: 'artwork', labelKey: 'next.artwork.family', cases});
