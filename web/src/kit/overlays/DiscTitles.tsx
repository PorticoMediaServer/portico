import type {RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {FileInfoDisc} from '@core/detail.ts';
import {durationText} from '@core/presentation/kit-model/text.ts';
import {SidePanel} from './SidePanel';
import {Button} from '../controls/Button';
import type {FileInfoMore} from './FileInfo';
import styles from './file-info.module.css';

const t = defaultI18n.t;

/** The owner's Titles panel (playback-info f15): one row per stored disc title, in
 * the scan's own order, naming the exact file the block was read for. */
export function DiscTitles({open, onOpenChange, disc, file, more, returnFocusRef}: {
  open: boolean; onOpenChange(open: boolean): void; disc?: FileInfoDisc; file?: string; more?: FileInfoMore; returnFocusRef: RefObject<HTMLElement | null>;
}) {
  return <SidePanel open={open} onOpenChange={onOpenChange} title={t('next.overlays.discTitles')} returnFocusRef={returnFocusRef} width={520}>
    {file && <p className={styles.note}>{file}</p>}
    <dl className={styles.files} aria-label={t('next.overlays.discTitles')}>{disc?.titles.map(title => <div key={title.number} className={styles.version}>
      <dt>{t('next.overlays.discTitle', {number: title.number})}</dt>
      <dd>{durationText(title.durationMs / 1000, t) ?? ''}{title.chapters > 0 && <>{' '}{t('next.overlays.discChapters', {count: title.chapters})}</>}</dd>
    </div>)}</dl>
    {more && <div className={styles.more}><Button variant="glass" size="xs" loading={more.busy} onClick={more.onPress}>{more.failed ? t('next.states.tryAgain') : t('next.overlays.fileInfoMoreTitles')}</Button></div>}
    <p className={styles.note}>{t('next.overlays.discTitlesNote')}</p>
  </SidePanel>;
}
