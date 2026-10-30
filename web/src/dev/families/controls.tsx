import {useState, type ReactNode} from 'react';
import {defaultI18n as i18n, formatMessage, type MessageId} from '@i18n';
import {controlsFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/controls-fixtures';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {Button, ResumeButton, Chip, Tabs, Segmented, Switch, TextField, Select, RichSelect, Slider, type ButtonProps, type ChipProps, type SelectOption} from '../../kit/controls';
import styles from '../../kit/controls/gallery.module.css';

const t = (key: MessageId) => i18n.t(key);
const fixture = (key: keyof typeof controlsFixtureMessages) => formatMessage(controlsFixtureMessages[key], {});
function Surface({children, stacked = false, raised = false}: {children: ReactNode; stacked?: boolean; raised?: boolean}) {
  return <div className={[stacked ? styles.stackedSpecimen : styles.specimen, raised && styles.raisedSpecimen].filter(Boolean).join(' ')}>{children}</div>;
}

function button(props: ButtonProps = {}, key: MessageId = 'next.controls.play', long = false) {
  return function ButtonCase() {
    const [activations, activate] = useState(0);
    return <Surface><Button {...props} onClick={() => activate(value => value + 1)} data-activations={activations}>{props.iconOnly ? undefined : long ? fixture('next.controls.fixture.longName') : t(key)}</Button></Surface>;
  };
}

function resume(glass = false, phone = false) {
  return function ResumeCase() {
    const [activations, activate] = useState(0);
    return <Surface><ResumeButton variant={glass ? 'glass' : 'primary'} size={glass ? 'small' : 'hero'} progress={glass ? .6 : .09} phone={phone} remaining={fixture(glass ? 'next.controls.fixture.remainingGlass' : 'next.controls.fixture.remaining')} onClick={() => activate(value => value + 1)} data-activations={activations}>{t('next.controls.resume')}</ResumeButton></Surface>;
  };
}

function chip(props: ChipProps = {}, key: MessageId = 'next.controls.season') {
  return function ChipCase() {
    const [selected, select] = useState(props.selected ?? false);
    const [activations, activate] = useState(0);
    return <Surface><Chip {...props} selected={props.selected === undefined ? undefined : selected} onClick={() => {select(value => !value); activate(value => value + 1);}} data-activations={activations}>{key === 'next.controls.season' ? fixture('next.controls.fixture.season') : t(key)}</Chip></Surface>;
  };
}

function tabs(phone = false, long = false) {
  return function TabsCase() {
    const [value, choose] = useState('episodes');
    return <Surface stacked><Tabs className={styles.fullWidth} phone={phone} aria-label={t('next.controls.details')} value={value} onValueChange={choose} items={[
      {value: 'episodes', label: long ? fixture('next.controls.fixture.longName') : t('next.controls.episodes'), panel: <span data-panel="episodes" />},
      {value: 'similar', label: t('next.controls.moreLikeThis'), panel: <span data-panel="similar" />},
      {value: 'details', label: t('next.controls.details'), panel: <span data-panel="details" />},
      {value: 'extras', label: t('next.controls.extras'), disabled: true},
    ]} /><output data-selection={value} /></Surface>;
  };
}

function segmented(variant: 'glass' | 'browse' | 'phone' = 'glass') {
  return function SegmentedCase() {
    const [value, choose] = useState(variant === 'glass' ? 'forced' : 'any');
    const items = variant === 'glass' ? [{value: 'off', label: t('next.controls.off')}, {value: 'forced', label: t('next.controls.forcedOnly')}, {value: 'always', label: t('next.controls.always')}] : variant === 'phone' ? [{value: 'any', label: t('next.controls.any')}, {value: 'yes', label: t('next.controls.yes')}, {value: 'no', label: t('next.controls.no')}] : [{value: 'any', label: t('next.controls.anyOfThese')}, {value: 'all', label: t('next.controls.allOfThese')}, {value: 'none', label: t('next.controls.noneOfThese')}];
    return <Surface raised><Segmented variant={variant} aria-label={t('next.controls.showSubtitles')} value={value} onValueChange={choose} items={items} /><output data-selection={value} /></Surface>;
  };
}

function switchCase(checked = false, size: 'compact' | 'phone' | 'episode' = 'compact', disabled = false) {
  return function SwitchCase() {
    const [value, choose] = useState(checked);
    return <Surface raised><Switch size={size} checked={value} disabled={disabled} onCheckedChange={choose} aria-label={t('next.controls.autoplay')} /><output data-selection={value} /></Surface>;
  };
}

function field(variant: 'field' | 'compact' | 'search' = 'field', state: 'value' | 'placeholder' | 'error' | 'disabled' | 'readonly' | 'long' | 'focus' = 'value') {
  return function FieldCase() {
    const [value, choose] = useState(state === 'placeholder' || state === 'error' ? '' : state === 'long' ? fixture('next.controls.fixture.longName') : variant === 'search' && state === 'focus' ? fixture('next.controls.fixture.settingsSearch') : t('next.controls.movies'));
    return <Surface stacked raised><TextField variant={variant} label={variant === 'search' ? undefined : t('next.controls.libraryName')}
      aria-label={variant === 'search' ? t('next.controls.searchSettings') : undefined} placeholder={t(variant === 'search' ? 'next.controls.searchSettings' : 'next.controls.namePlaceholder')}
      value={value} onChange={event => choose(event.currentTarget.value)} autoFocus={state === 'focus'} disabled={state === 'disabled'} readOnly={state === 'readonly'} error={state === 'error' ? t('next.controls.nameError') : undefined}
      trailing={variant === 'search' && value ? <Button variant="icon" icon="i-x" iconSize={14} iconOnly aria-label={t('next.controls.clearSearch')} className={styles.clearSearch} onClick={() => choose('')} /> : undefined} /><output data-value={value} /></Surface>;
  };
}

function AuthFieldCase() {
  const [value, choose] = useState(fixture('next.controls.fixture.server'));
  return <Surface stacked><TextField variant="auth" label={t('next.controls.serverName')} value={value} onChange={event => choose(event.currentTarget.value)} /></Surface>;
}
function ViewSegmentedCase() {
  const [value, choose] = useState('guide');
  return <Surface><Segmented variant="view" aria-label={t('next.controls.layout')} value={value} onValueChange={choose} items={[
    {value: 'list', label: t('next.controls.list'), icon: 'i-list-guide'},
    {value: 'guide', label: t('next.controls.guide'), icon: 'i-timeline'},
  ]} /><output data-selection={value} /></Surface>;
}
function CodeFieldCase() {
  const [value, choose] = useState(fixture('next.controls.fixture.code'));
  return <Surface stacked raised><TextField variant="code" label={t('next.controls.codeFromHost')} value={value} onChange={event => choose(event.currentTarget.value)} error={t('next.controls.codeError')} errorIcon="i-warn" /></Surface>;
}
function LargeSearchCase() {
  const [value, choose] = useState('');
  return <Surface stacked><TextField variant="large-search" type="search" aria-label={t('next.controls.search')} placeholder={t('next.controls.searchLibraries')} value={value} onChange={event => choose(event.currentTarget.value)} trailing={<span className={styles.searchHint}><span className={styles.keyHint}>{t('next.controls.searchShortcut')}</span>{t('next.controls.searchFromAnywhere')}</span>} autoFocus /></Surface>;
}

function ButtonContractCase() {
  const [clicks, click] = useState(0);
  const [submissions, submit] = useState(0);
  return <Surface><form onSubmit={event => {event.preventDefault(); submit(count => count + 1);}}>
    <Button variant="primary" type="submit" icon="i-play" data-dom-prop="forwarded"
      ref={element => {if (element) element.dataset.refConnected = String(element instanceof HTMLButtonElement);}}
      onClick={() => click(count => count + 1)}>{t('next.controls.play')}</Button>
    <output data-clicks={clicks} data-submissions={submissions} />
  </form></Surface>;
}

const qualityOptions = (): readonly SelectOption[] => [
  {value: 'original', label: t('next.controls.original'), description: t('next.controls.originalDescription'), isDefault: true},
  {value: 'auto', label: t('next.controls.automatic'), description: t('next.controls.automaticDescription')},
  {value: '4k', label: t('next.controls.quality4k'), description: t('next.controls.limited1080'), disabled: true, separatorBefore: true},
  {value: '1440', label: t('next.controls.quality1440'), description: t('next.controls.limited1080'), disabled: true},
  {value: '1080', label: t('next.controls.quality1080'), description: t('next.controls.data1080')},
  {value: '720', label: t('next.controls.quality720'), description: t('next.controls.data720')},
  {value: '480', label: t('next.controls.quality480'), description: t('next.controls.data480')},
  {value: '360', label: t('next.controls.quality360'), description: t('next.controls.data360')},
];

function select(rich = false, opened = false, disabled = false, long = false) {
  return function SelectCase() {
    const [value, choose] = useState(rich ? opened ? 'auto' : 'original' : '10');
    const [open, setOpen] = useState(opened);
    const Component = rich ? RichSelect : Select;
    return <Surface stacked raised><Component aria-label={t(rich ? 'next.controls.away' : 'next.controls.stillWatching')} value={value} onValueChange={choose} open={open} onOpenChange={setOpen} disabled={disabled}
      options={rich ? qualityOptions() : [{value: '10', label: long ? fixture('next.controls.fixture.longName') : t('next.controls.tenSeconds')}, {value: '30', label: t('next.controls.thirtySeconds')}, {value: 'three', label: t('next.controls.afterThree')}]} /><output data-selection={value} data-open={open} /></Surface>;
  };
}

function volume(variant: 'player' | 'dock' = 'player', disabled = false) {
  return function VolumeCase() {
    const [value, choose] = useState(70);
    const [committed, commit] = useState(70);
    return <Surface><Slider aria-label={t('next.controls.volume')} variant={variant} min={0} max={100} step={1} value={value} onValueChange={choose} onValueCommit={commit} disabled={disabled} /><output data-volume={value} data-committed={committed} /></Surface>;
  };
}

function InteractionCase() {
  const [buttonCount, click] = useState(0);
  const [checked, check] = useState(false);
  const [chipSelected, selectChip] = useState(false);
  const [text, setText] = useState('');
  const [value, choose] = useState('off');
  const [quality, chooseQuality] = useState('auto');
  const [volumeValue, chooseVolume] = useState(50);
  const [committed, commitVolume] = useState(50);
  return <Surface stacked>
    <Button variant="primary" icon="i-play" onClick={() => click(count => count + 1)} data-activations={buttonCount}>{t('next.controls.play')}</Button>
    <Button disabled onClick={() => click(count => count + 1000)}>{t('next.controls.disabled')}</Button>
    <Button loading onClick={() => click(count => count + 1000)}>{t('next.controls.loading')}</Button>
    <Chip selected={chipSelected} count={0} onClick={() => selectChip(selected => !selected)}>{t('next.controls.filters')}</Chip>
    <Switch aria-label={t('next.controls.autoplay')} checked={checked} onCheckedChange={check} />
    <TextField label={t('next.controls.name')} value={text} onChange={event => setText(event.currentTarget.value)} />
    <Segmented aria-label={t('next.controls.showSubtitles')} value={value} onValueChange={choose} items={[{value: 'off', label: t('next.controls.off')}, {value: 'disabled', label: t('next.controls.disabled'), disabled: true}, {value: 'always', label: t('next.controls.always')}]} />
    <RichSelect aria-label={t('next.controls.quality')} options={qualityOptions()} value={quality} onValueChange={chooseQuality} />
    <Slider aria-label={t('next.controls.volume')} value={volumeValue} onValueChange={chooseVolume} onValueCommit={commitVolume} />
    <output data-button-count={buttonCount} data-checked={checked} data-chip={chipSelected} data-text={text} data-segment={value} data-quality={quality} data-volume={volumeValue} data-committed={committed} />
    <form data-form="uncontrolled"><Switch name="autoplay" defaultChecked aria-label={t('next.controls.hideWatched')} /><TextField name="library" label={t('next.controls.libraryName')} defaultValue={t('next.controls.movies')} /><Select name="quality" aria-label={t('next.controls.away')} defaultValue="original" options={qualityOptions()} /><Button type="reset">{t('next.controls.reset')}</Button></form>
  </Surface>;
}

function UncontrolledCase() {
  const [chipSelected, reportChip] = useState(false);
  const [tab, reportTab] = useState('episodes');
  const [segment, reportSegment] = useState('off');
  const [committed, commit] = useState(35);
  const [submitted, submit] = useState('');
  return <form className={styles.stackedSpecimen} aria-label={t('next.controls.playback')} data-form="uncontrolled"
    onSubmit={event => {event.preventDefault(); submit(JSON.stringify([...new FormData(event.currentTarget).entries()]));}}>
    <Chip defaultSelected={false} onSelectedChange={reportChip}>{t('next.controls.filters')}</Chip>
    <Tabs aria-label={t('next.controls.details')} defaultValue="episodes" onValueChange={reportTab} items={[{value: 'episodes', label: t('next.controls.episodes'), panel: <output data-panel="episodes" />}, {value: 'details', label: t('next.controls.details'), panel: <output data-panel="details" />}]} />
    <Segmented name="subtitles" aria-label={t('next.controls.showSubtitles')} defaultValue="off" onValueChange={reportSegment} items={[{value: 'off', label: t('next.controls.off')}, {value: 'always', label: t('next.controls.always')}]} />
    <Switch name="autoplay" defaultChecked aria-label={t('next.controls.autoplay')} />
    <TextField name="library" label={t('next.controls.name')} defaultValue={t('next.controls.movies')} required />
    <Select name="quality" aria-label={t('next.controls.quality')} defaultValue="original" options={qualityOptions()} />
    <Slider name="volume" aria-label={t('next.controls.volume')} defaultValue={35} onValueCommit={commit} />
    <Button type="reset">{t('next.controls.reset')}</Button><Button type="submit" variant="primary">{t('next.controls.play')}</Button>
    <output data-chip={chipSelected} data-tab={tab} data-segment={segment} data-committed={committed} data-submitted={submitted} />
  </form>;
}

const cases: readonly GalleryCase[] = [
  {id: 'primary', labelKey: 'next.controls.play', width: 240, height: 80, render: button({variant: 'primary', icon: 'i-play'})},
  {id: 'glass', labelKey: 'next.controls.myList', width: 240, height: 80, render: button({icon: 'i-plus'}, 'next.controls.myList')},
  {id: 'round', labelKey: 'next.controls.more', width: 80, height: 80, render: button({icon: 'i-more', iconOnly: true, 'aria-label': t('next.controls.more')})},
  {id: 'icon', labelKey: 'next.controls.search', width: 64, height: 64, render: button({variant: 'icon', icon: 'i-search', iconOnly: true, 'aria-label': t('next.controls.search')})},
  {id: 'text', labelKey: 'next.controls.acknowledge', width: 150, height: 64, render: button({variant: 'text'}, 'next.controls.acknowledge')},
  {id: 'hero-phone', labelKey: 'next.controls.play', width: 240, height: 70, render: button({variant: 'primary', icon: 'i-play', phone: true})},
  {id: 'compact', labelKey: 'next.controls.openCollection', width: 240, height: 70, render: button({variant: 'glass', size: 'compact'}, 'next.controls.openCollection')},
  {id: 'small', labelKey: 'next.controls.play', width: 240, height: 66, render: button({variant: 'primary', icon: 'i-play', size: 'small'})},
  {id: 'xs', labelKey: 'next.controls.openRecordings', width: 240, height: 58, render: button({variant: 'glass', size: 'xs'}, 'next.controls.openRecordings')},
  {id: 'episode', labelKey: 'next.controls.play', width: 240, height: 72, render: button({variant: 'primary', icon: 'i-play', size: 'episode'})},
  {id: 'dock-button', labelKey: 'next.controls.stop', width: 240, height: 62, render: button({variant: 'glass', size: 'dock'}, 'next.controls.stop')},
  {id: 'resume', labelKey: 'next.controls.resume', width: 380, height: 80, render: resume()},
  {id: 'resume-glass', labelKey: 'next.controls.resume', width: 380, height: 80, render: resume(true)},
  {id: 'resume-phone', labelKey: 'next.controls.resume', width: 350, height: 70, render: resume(false, true)},
  {id: 'disabled', labelKey: 'next.controls.disabled', width: 240, height: 80, render: button({variant: 'primary', icon: 'i-play', disabled: true})},
  {id: 'loading', labelKey: 'next.controls.loading', width: 240, height: 80, render: button({variant: 'primary', icon: 'i-play', loading: true})},
  {id: 'loading-text', labelKey: 'next.controls.loading', width: 150, height: 64, render: button({variant: 'text', loading: true}, 'next.controls.acknowledge')},
  {id: 'ghost-disabled', labelKey: 'next.controls.saveView', width: 240, height: 64, render: button({variant: 'ghost', icon: 'i-save', disabled: true}, 'next.controls.saveView')},
  {id: 'segmented-view', labelKey: 'next.controls.layout', width: 240, height: 64, render: ViewSegmentedCase},
  {id: 'long-name', labelKey: 'next.controls.name', width: 240, height: 80, render: button({variant: 'primary', icon: 'i-play'}, 'next.controls.play', true)},
  {id: 'chip', labelKey: 'next.controls.season', width: 240, height: 64, render: chip({trailingIcon: 'i-down'})},
  {id: 'chip-selected', labelKey: 'next.controls.selected', width: 240, height: 64, render: chip({variant: 'search', selected: true}, 'next.controls.all')},
  {id: 'chip-count', labelKey: 'next.controls.filters', width: 240, height: 64, render: chip({variant: 'toolbar', icon: 'i-filter', iconSize: 18, count: 2, countStyle: 'badge'}, 'next.controls.filters')},
  {id: 'chip-type-count', labelKey: 'next.controls.movies', width: 240, height: 64, render: chip({variant: 'search', count: 7}, 'next.controls.movies')},
  {id: 'chip-dim', labelKey: 'next.controls.people', width: 240, height: 64, render: chip({variant: 'search', dim: true, disabled: true}, 'next.controls.people')},
  {id: 'chip-retry', labelKey: 'next.controls.people', width: 240, height: 64, render: chip({variant: 'search', retry: true, 'aria-label': t('next.controls.peopleRetry')}, 'next.controls.people')},
  {id: 'chip-download-retry', labelKey: 'next.controls.tryAgain', width: 240, height: 54, render: chip({variant: 'download', retry: true, phone: true}, 'next.controls.tryAgain')},
  {id: 'chip-phone', labelKey: 'next.controls.all', width: 240, height: 58, render: chip({phone: true, selected: true, count: 6}, 'next.controls.all')},
  {id: 'chip-quick', labelKey: 'next.controls.all', width: 240, height: 64, render: chip({variant: 'quick', selected: true, count: 72}, 'next.controls.all')},
  {id: 'chip-facet', labelKey: 'next.controls.animation', width: 240, height: 58, render: chip({variant: 'facet', selected: true, count: 6, icon: 'i-check'}, 'next.controls.animation')},
  {id: 'chip-applied', labelKey: 'next.controls.genre', width: 280, height: 58, render: chip({variant: 'applied', prefix: t('next.controls.genre'), trailingIcon: 'i-close', 'aria-label': t('next.controls.removeAnimation')}, 'next.controls.animation')},
  {id: 'chip-pill', labelKey: 'next.controls.quality', width: 240, height: 58, render: chip({variant: 'pill', prefix: t('next.controls.auto'), 'aria-label': fixture('next.controls.fixture.qualityLabel')}, 'next.controls.quality1080')},
  {id: 'tabs', labelKey: 'next.controls.episodes', width: 600, height: 90, render: tabs()},
  {id: 'tabs-phone', labelKey: 'next.controls.episodes', width: 390, height: 90, render: tabs(true)},
  {id: 'tabs-long', labelKey: 'next.controls.details', width: 390, height: 90, render: tabs(true, true)},
  {id: 'segmented', labelKey: 'next.controls.showSubtitles', width: 300, height: 64, render: segmented()},
  {id: 'segmented-browse', labelKey: 'next.controls.layout', width: 360, height: 62, render: segmented('browse')},
  {id: 'segmented-phone', labelKey: 'next.controls.layout', width: 280, height: 58, render: segmented('phone')},
  {id: 'switch-off', labelKey: 'next.controls.off', width: 80, height: 54, render: switchCase()},
  {id: 'switch-on', labelKey: 'next.controls.autoplay', width: 80, height: 54, render: switchCase(true)},
  {id: 'switch-phone', labelKey: 'next.controls.autoplay', width: 90, height: 60, render: switchCase(true, 'phone')},
  {id: 'switch-episode', labelKey: 'next.controls.hideWatched', width: 80, height: 54, render: switchCase(false, 'episode')},
  {id: 'switch-disabled', labelKey: 'next.controls.disabled', width: 80, height: 54, render: switchCase(true, 'compact', true)},
  {id: 'field', labelKey: 'next.controls.libraryName', width: 360, height: 100, render: field()},
  {id: 'field-focus', labelKey: 'next.controls.focus', width: 360, height: 100, render: field('field', 'focus')},
  {id: 'field-auth', labelKey: 'next.controls.serverName', width: 480, height: 106, render: AuthFieldCase},
  {id: 'field-code', labelKey: 'next.controls.codeFromHost', width: 412, height: 180, render: CodeFieldCase},
  {id: 'field-compact', labelKey: 'next.controls.libraryName', width: 360, height: 96, render: field('compact')},
  {id: 'field-error', labelKey: 'next.controls.nameError', width: 360, height: 126, render: field('field', 'error')},
  {id: 'field-disabled', labelKey: 'next.controls.disabled', width: 360, height: 100, render: field('field', 'disabled')},
  {id: 'field-readonly', labelKey: 'next.controls.libraryName', width: 360, height: 100, render: field('field', 'readonly')},
  {id: 'field-placeholder', labelKey: 'next.controls.libraryName', width: 360, height: 100, render: field('field', 'placeholder')},
  {id: 'field-long', labelKey: 'next.controls.name', width: 360, height: 100, render: field('field', 'long')},
  {id: 'search', labelKey: 'next.controls.searchSettings', width: 320, height: 70, render: field('search', 'placeholder')},
  {id: 'search-focus', labelKey: 'next.controls.focus', width: 320, height: 70, render: field('search', 'focus')},
  {id: 'large-search', labelKey: 'next.controls.search', width: 900, height: 112, render: LargeSearchCase},
  {id: 'select', labelKey: 'next.controls.stillWatching', width: 220, height: 64, render: select()},
  {id: 'select-open', labelKey: 'next.controls.stillWatching', width: 320, height: 240, render: select(false, true)},
  {id: 'select-disabled', labelKey: 'next.controls.disabled', width: 220, height: 64, render: select(false, false, true)},
  {id: 'select-long', labelKey: 'next.controls.name', width: 240, height: 64, render: select(false, false, false, true)},
  {id: 'rich-select', labelKey: 'next.controls.away', width: 260, height: 64, render: select(true)},
  {id: 'rich-select-open', labelKey: 'next.controls.away', width: 380, height: 480, render: select(true, true)},
  {id: 'rich-select-phone', labelKey: 'next.controls.away', width: 390, height: 844, render: select(true, true)},
  {id: 'volume', labelKey: 'next.controls.volume', width: 116, height: 48, render: volume()},
  {id: 'volume-dock', labelKey: 'next.controls.volume', width: 124, height: 48, render: volume('dock')},
  {id: 'volume-disabled', labelKey: 'next.controls.disabled', width: 116, height: 48, render: volume('player', true)},
  {id: 'interactions', labelKey: 'next.controls.interactions', width: 390, height: 1000, render: InteractionCase},
  {id: 'button-contract', labelKey: 'next.controls.interactions', width: 240, height: 80, render: ButtonContractCase},
  {id: 'uncontrolled', labelKey: 'next.controls.interactions', width: 390, height: 900, render: UncontrolledCase},
];

export default defineGalleryFamily({id: 'controls', labelKey: 'next.controls.family', cases});
