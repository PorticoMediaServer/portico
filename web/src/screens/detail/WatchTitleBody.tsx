import {TitleBody} from './Detail';
import {ShowBody} from './Show';
import type {WatchBodyBindings} from './watch-body';

/** No route, media element or lifetime reset. The persistent Watch host owns load/dispose. */
export function WatchTitleBody({binding, compact = true}: {binding: WatchBodyBindings; compact?: boolean}) {
  return binding.body.kind === 'show' ? <ShowBody binding={binding.body.binding} compact={compact}/>
    : <TitleBody binding={binding.body.binding} compact={compact}/>;
}
