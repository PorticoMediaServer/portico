import {mergeRevalidatedList} from '@core/freshness-actions.ts';
import type {SettingsPeoplePage} from '@core/settings-people.ts';

/** Only explicitly loaded pages are retained. Quiet refresh never accumulates a
 * history of displaced first pages or borrows the old continuation. */
export function revalidatedPeoplePage(shown: SettingsPeoplePage, fresh: SettingsPeoplePage, firstPageIds: ReadonlySet<string>): SettingsPeoplePage {
  const oldFirst = shown.items.filter(row => firstPageIds.has(row.accountId));
  const first = mergeRevalidatedList(oldFirst, fresh.items, row => row.accountId,
    {same: (a,b) => JSON.stringify(a) === JSON.stringify(b), complete: true});
  const freshIds = new Set(first.map(row => row.accountId));
  const tail = shown.items.filter(row => !firstPageIds.has(row.accountId) && !freshIds.has(row.accountId));
  return Object.freeze({...fresh, items: Object.freeze([...first, ...tail])});
}

import type {SettingsPeopleClient, SettingsPeopleQuery} from '@core/settings-people.ts';
import type {AccessMember} from '@core/server-administration.ts';
import type {QuietInput} from './quiet-refresh';
export type PeopleRefreshSnapshot = Readonly<{page?:SettingsPeoplePage;query:SettingsPeopleQuery;personId?:string;person?:AccessMember}>;
/** The mounted table alone installs reads. Independent detail debt includes a
 * selected tail row, even when that row is absent from the new first page. */
export function peopleQuietInputs(o: Readonly<{
  client:SettingsPeopleClient;get:()=>PeopleRefreshSnapshot;serial:()=>number;current:()=>boolean;blocked:()=>boolean;
  subscribe:(listener:()=>void)=>()=>void;firstPageIds:()=>ReadonlySet<string>;
  installPage:(page:SettingsPeoplePage,firstPageIds:ReadonlySet<string>)=>void;
  installPerson:(person:AccessMember)=>void;closePerson:(id:string)=>void;denied:(error:unknown)=>void;
  requests:Set<AbortController>;
}>):readonly QuietInput[] {
  const active=(serial:number) => o.current() && !o.blocked() && o.serial()===serial;
  const running: Partial<Record<'query'|'detail',AbortController>>={};
  const read=async <T>(lane:'query'|'detail',fetch:(signal:AbortSignal)=>Promise<T>,install:(value:T)=>void,owns:()=>boolean) => {
    const request=new AbortController();running[lane]=request;o.requests.add(request);
    try {const next=await fetch(request.signal);if(request.signal.aborted||!owns())return {kind:'deferred'} as const;install(next);return {kind:'adopted'} as const;}
    catch(error){if(request.signal.aborted||!owns())return {kind:'deferred'} as const;if([401,403].includes((error as {status?:number})?.status??0))o.denied(error);return {kind:'unreadable',error} as const;}
    finally{o.requests.delete(request);if(running[lane]===request)delete running[lane];}
  };
  const common={subscribe:o.subscribe,blocked:o.blocked,settlement:o.serial};
  return [
    {...common,id:'query',cancel:()=>running.query?.abort(),refresh:async()=>{
      const before=o.get(),serial=o.serial();
      if(!active(serial)||!before.page)return {kind:'deferred'};
      return read('query',signal=>o.client.refresh(before.query,signal),fresh=>o.installPage(revalidatedPeoplePage(before.page!,fresh,o.firstPageIds()),new Set(fresh.items.map(row=>row.accountId))),
        ()=>active(serial)&&o.get().page===before.page&&JSON.stringify(o.get().query)===JSON.stringify(before.query));
    }},
    {...common,id:'selected-detail',cancel:()=>running.detail?.abort(),refresh:async()=>{
      const before=o.get(),serial=o.serial(),id=before.personId;
      if(!active(serial))return {kind:'deferred'};
      if(!id)return {kind:'adopted'};
      const owns=()=>active(serial)&&o.get().personId===id;
      return read('detail',async signal=>{
        try{return await o.client.person(id,signal);}catch(error){if((error as {status?:number})?.status===404&&owns())return undefined;throw error;}
      },person=>person?o.installPerson(person):o.closePerson(id),owns);
    }},
  ];
}
