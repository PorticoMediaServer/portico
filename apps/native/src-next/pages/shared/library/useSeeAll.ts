import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {appliedFilterParts, browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {hasHomeRowQueue} from '../../../../../../packages/client-core/src/home-row-browse';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {homeBrowseLabel} from '../../../screens/home/browse-label';
import {homeSeeAllQuickOrder} from '../../../screens/home/see-all-model';
import type {HomeSeeAllProps} from '../../../screens/home/HomeSeeAll';
import {authorityKey} from '../../../shell/model';
import {useTheme} from '../../../kit/theme';
import {browseFailure} from './useLibrary';

export type SeeAllProps = HomeSeeAllProps;

export function useSeeAll({controller, runtime, active}: SeeAllProps) {
  const {isTV} = useTheme(), t = runtime.text.t, catalog = {t, has: defaultI18n.has};
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const data = useSyncExternalStore(controller.collection.subscribe, controller.collection.getSnapshot);
  const [view, setView] = useState<'grid' | 'list'>('grid');
  const visible = useRef(active);
  visible.current = active;
  useEffect(() => {void controller.load();}, [controller]);
  const retry = () => {void controller.load();};
  const row = state.row, ready = !!row && !!state.capabilities && !!state.sort;
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    setSlow(false);
    if (ready || !active || state.phase === 'error') return;
    const timer = setTimeout(() => setSlow(true), 8000);
    return () => clearTimeout(timer);
  }, [ready, active, state.phase]);
  const title = row ? serverTextLabel(catalog, row.titleText ?? {key: `library.landing.${row.id}`, fallback: row.title}, row.title) : t('next.home.seeAll');
  const reason = row?.reason?.text ?? row?.why ?? row?.subtitleText;
  const intro = reason ? serverTextLabel(catalog, reason) : undefined;
  const artwork = row?.entries.find(entry => entry.backdropUrl || entry.posterUrl);
  const total = state.counts.find(count => count.id === (state.selectedKind ?? 'all'));
  const model = state.capabilities && state.sort && browseToolbarModel(state.capabilities, state.capabilities.library.kind || 'movies', state.filters, state.sort, {
    ...runtime.text, textEntry: !isTV, scope: 'group', owner: runtime.getSnapshot().account.viewerRole === 'owner',
  });
  const label = (value: Parameters<typeof homeBrowseLabel>[0]) => homeBrowseLabel(value, catalog);
  const labelCount = (id: string) => state.counts.find(count => count.id === id)?.total;
  const onQuickChange = (id: string, checked: boolean) => {
    const quick = model?.quick.find(value => value.id === id);
    if (!quick) return;
    const remaining = state.filters.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query));
    controller.setFilters(checked ? [...remaining, quick.query] : remaining);
  };
  const focusId = isTV ? `home-see-all:${authorityKey(runtime.getSnapshot().authority)}:${controller.rowId}` : undefined;
  const playable = !!row && hasHomeRowQueue(row.id);
  const labelField = (id: string) => {
    const field = state.capabilities?.fields.find(candidate => candidate.id === id);
    return field ? label(field) : id;
  };
  const applied = model?.applied.flatMap(node => 'field' in node ? [{id: JSON.stringify(node), parts: appliedFilterParts(node, labelField, runtime.text)}] : []) ?? [];
  const clear = () => {controller.setFilters([]); controller.selectKind(undefined);};
  const remove = (id: string) => controller.setFilters(state.filters.filter(node => JSON.stringify(node) !== id));
  const focus = focusId ? {
    header: {id: focusId, enabled: active, entry: playable ? 'play' : 'back', edges: {down: `${focusId}.toolbar`}},
    toolbar: {id: `${focusId}.toolbar`, enabled: active, edges: {up: focusId, down: applied.length ? `${focusId}.applied` : `${focusId}.grid`}},
    applied: {id: `${focusId}.applied`, enabled: active, edges: {up: `${focusId}.toolbar`, down: `${focusId}.grid`}},
    grid: {id: `${focusId}.grid`, enabled: active, edges: {up: applied.length ? `${focusId}.applied` : `${focusId}.toolbar`},
      count: data.total, screenStart: {regionId: focusId, childId: playable ? 'play' : 'back'}},
  } : undefined;
  // Back names the page under this one: the library it was opened from, or Home.
  const origin = runtime.getSnapshot().destination;
  const backLabel = origin.kind === 'library' ? runtime.getSnapshot().libraries.find(library => library.id === origin.libraryId)?.name ?? t('next.home.shell.home') : t('next.home.shell.home');
  return {t, title, intro, focus, backLabel, clear, remove, applied, view, setView, ready, slow, data, labels: {field: label, quick: label, sort: label}, onQuickChange,
    model: model && {...model, applied: state.filters, quick: model.quick.filter(quick => homeSeeAllQuickOrder(quick.id, isTV))},
    failure: state.phase === 'error' ? browseFailure(runtime.error(state.error, retry, 'home')) : undefined,
    gridFailure: data.status === 'error' ? runtime.error(data.error, () => controller.collection.ensureRange(0, 0), 'home') : undefined,
    empty: data.status === 'ready' && data.total === 0,
    total: total?.total,
    ambient: artwork ? runtime.ambient(artwork.backdropUrl ?? artwork.posterUrl) : undefined,
    playable,
    kinds: controller.kinds.length ? [{id: 'all', label: t('search.chip.all'), count: labelCount('all'), selected: !state.selectedKind,
      onPress: () => controller.selectKind(undefined)}, ...controller.kinds.map(kind => ({id: kind.id, count: labelCount(kind.id),
      selected: state.selectedKind === kind.id, onPress: () => controller.selectKind(kind.id)}))] : [],
    isCurrent: (item: NonNullable<ReturnType<typeof controller.collection.itemAt>>, index: number) => visible.current && controller.isCurrentItem(item, index),
  };
}
