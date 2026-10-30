import type {RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {DownloadOptionsView} from '@core/downloads.ts';
import {Artwork, type ArtworkSource} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {t} from './text';
import styles from './download-notice.module.css';

/** Frame 36c presentation only. The browser producer owns preparation, grants
 * and authenticated Save file; this component owns no store or transport. */
export function DownloadNotice({open, onOpenChange, title, artwork, options, busy = false, onChoose, returnFocusRef}: Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  artwork?: ArtworkSource;
  options: DownloadOptionsView;
  busy?: boolean;
  onChoose: (quality: string) => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>) {
  return <BottomSheet open={open} onOpenChange={onOpenChange} fitContent showCloseButton={false} returnFocusRef={returnFocusRef}
    accessibilityLabel={t('next.detail.download')} className={styles.sheet} headerClassName={styles.header} bodyClassName={styles.body}
    title={<span className={styles.title}>{artwork && <Artwork source={artwork} alt="" className={styles.poster} />}<span><b>{t('next.detail.download')}</b><small>{title}</small></span></span>}
    headerAction={<Button variant="icon" size="small" icon="i-close" iconOnly aria-label={t('next.states.close')} onClick={() => onOpenChange(false)} />}>
    <p className={styles.section}>{t('next.detail.downloadQuality')}</p>
    <div className={styles.options}>{options.options.map(option => <button key={option.quality} type="button" className={styles.option}
      disabled={busy || !options.policy.allowDownloads || !option.available} onClick={() => onChoose(option.quality)}>
      <span className={styles.copy}><b>{option.kind === 'source' ? t('web.downloads.original') : option.label}</b><small>{t(option.kind === 'source' ? 'next.detail.downloadOriginalFile' : 'next.detail.downloadSmaller')}</small></span>
      <span className={styles.size}>{option.estimatedBytes > 0 ? option.estimated ? t('web.downloads.about', {size: defaultI18n.bytes(option.estimatedBytes)}) : defaultI18n.bytes(option.estimatedBytes) : null}
        {option.requiresPreparation && <small>{t('next.detail.downloadPreparesFirst')}</small>}</span>
      <span className={styles.download} aria-hidden="true"><Icon name="i-download" size={18} /></span>
    </button>)}</div>
  </BottomSheet>;
}
