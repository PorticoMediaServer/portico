import {useGenreBrowse, type GenreBrowseProps} from './useGenreBrowse';

/** A genre from Search's Browse by genre on TV: the shared genre read, plus the page's three focus regions (Back, the
 * toolbar, the grid), Down and Up between them, as the See all page has. The page is named by its route's focus scope. */
export function useTVGenreBrowse(props: GenreBrowseProps) {
  const view = useGenreBrowse(props), id = props.focusScope, enabled = view.active.visible;
  const focus = {
    header: {id, enabled, entry: 'back', edges: {down: `${id}.toolbar`}},
    toolbar: {id: `${id}.toolbar`, enabled, edges: {up: id, down: `${id}.grid`}},
    grid: {id: `${id}.grid`, enabled, edges: {up: `${id}.toolbar`}, count: view.total, screenStart: {regionId: id, childId: 'back'}},
  };
  return {...view, focus};
}
