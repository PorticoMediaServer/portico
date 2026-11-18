import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {BottomSheet, Dialog, SidePanel} from '../overlays';
import {Button} from '../controls';
import {Skeleton} from '../states';
import styles from './filters-surface.module.css';

const t = defaultI18n.t;
export type FiltersSurfaceForm = 'sheet' | 'panel' | 'docked';
export type FiltersSurfaceProps = Readonly<{
  /** A sheet at phone width (phone-app 6), a side panel over the page (desktop-browse filters), or docked beside the grid on a
   * touch tablet (tablet 4). `BrowseFrame` picks it from the form. */
  form: FiltersSurfaceForm;
  /** The upright tablet's narrower panel (tablet 5). */
  medium?: boolean;
  open: boolean; onClose: () => void; onClear: () => void;
  /** "6 movies": the panel's "6 movies match" and the sheet's "Show 6 movies". Absent while the count is on its way. */
  count?: string;
  /** A sheet's Save view beside its primary. */
  save?: Readonly<{canSave: boolean; onSave: () => void}>;
  /** The fields: the kit `FilterBuilder`. */
  children: ReactNode;
}>;

/** The Filters surface (moved from screens/browse `BrowseFilters`, which draws it): the title, the count, the fields and the
 * foot, as the form's surface. Done closes; on a sheet the primary shows what will be shown. */
export function FiltersSurface({form, medium = false, open, onClose, onClear, count, save, children}: FiltersSurfaceProps) {
  const change = (opened: boolean) => {if (!opened) onClose();};
  const match = count ? <span className={styles.filterCount}><b>{count}</b> {t('next.browse.match')}</span> : undefined;
  const footer = <><Button variant="glass" size="hero" onClick={onClear}>{t('next.browse.clearAll')}</Button><Button variant="primary" size="hero" onClick={onClose}>{t('action.done')}</Button></>;
  if (form === 'docked') return <SidePanel open={open} onOpenChange={change} title={t('next.browse.filters')} width={380} topInset={0} className={styles.dockedFilters} description={match} footer={footer}>{children}</SidePanel>;
  if (form === 'sheet') return <BottomSheet open={open} onOpenChange={change} showCloseButton={false} detent={1}
    headerClassName={styles.phoneFilterHeader} title={<span className={styles.filterTitle}>{t('next.browse.filters')}<Button variant="text" size="small" onClick={onClear}>{t('next.browse.clearAll')}</Button></span>}
    footerClassName={styles.filterFooter} footer={<>{save && <Button variant="glass" size="hero" disabled={!save.canSave} onClick={save.onSave}>{t('next.browse.saveView')}</Button>}<Button variant="primary" size="hero" disabled={!count} aria-label={count ? undefined : t('next.browse.filters')} onClick={onClose}>{count ? t('next.browse.showCount', {count}) : <Skeleton width={116} height={20}/>}</Button></>}>{children}</BottomSheet>;
  return <Dialog responsive={false} open={open} onOpenChange={change} title={t('next.browse.filters')} placement="right" width={medium ? 420 : 520} closeIconSize={22} closeLabel={t('next.browse.closeFilters')}
    headerClassName={styles.filterHeader} bodyClassName={styles.filterBody} footerClassName={styles.filterFooter} description={match} footer={footer}>{children}</Dialog>;
}
