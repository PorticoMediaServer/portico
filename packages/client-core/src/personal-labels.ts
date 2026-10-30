/** Viewer-facing labels for reconciliation of changes authored on multiple devices. */
export function personalFieldLabel(field:string):string {
  return ({watchlisted:'My List',watchlist:'My List',favorite:'Favorite',watched:'Watched status',rating:'Rating'} as Record<string,string>)[field]??'Saved preference';
}
export function personalChoiceLabel(field:string,value:unknown):string {
  if(field==='rating')return value===null?'Not rated':typeof value==='number'&&Number.isFinite(value)?`${value / 2} / 5`:'Saved rating';
  if(typeof value==='boolean') {
    if(field==='watchlisted'||field==='watchlist')return value?'In My List':'Not in My List';
    if(field==='favorite')return value?'Favorite':'Not a favorite';
    if(field==='watched')return value?'Watched':'Unwatched';
    return value?'On':'Off';
  }
  return value===null?'Not set':'Saved choice';
}
export function personalChoiceDate(authoredAt:string):string {
  const date=new Date(authoredAt);
  return Number.isFinite(date.getTime())?date.toLocaleString(undefined,{dateStyle:'medium',timeStyle:'short'}):'Another device';
}
