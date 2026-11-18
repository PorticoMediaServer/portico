import {useRef,useState,useSyncExternalStore} from 'react';
import {WatchTogetherPill} from '../../kit/shell';
import {ProfileFace} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {Button} from '../../kit/controls';
import {Popover} from '../../kit/overlays';
import type {TogetherBindings} from '../together/bindings';
import type {WebPlaybackRuntimeSnapshot,WebShellPlaybackRuntime} from '../../platform/playback';
import {memberName, memberStatusKey} from '../together/together-model';
import {roomActionAllowed} from './room-guard';
import styles from './room-pill.module.css';
const noSubscribe=()=>()=>{},noRoom=()=>null;
/** The current real room only; the pill never creates a room, member, clock or colour fact. */
export function WatchRoomPill({binding,runtime,snapshot,reportError}:{binding:TogetherBindings|undefined;runtime:WebShellPlaybackRuntime;snapshot:WebPlaybackRuntimeSnapshot;reportError(error:unknown):void}){
 const room=useSyncExternalStore(binding?.subscribe??noSubscribe,binding?.getSnapshot??noRoom,noRoom),[open,setOpen]=useState(false),anchor=useRef<HTMLSpanElement>(null),button=useRef<HTMLButtonElement|null>(null);
 const group=room?.room.group,fence=snapshot.fence;
 if(!binding?.isCurrent()||!group||!['live','reconnecting'].includes(room!.room.phase)||!fence||fence.kind!=='queue'
  ||!runtime.groupPlaybackMatches(fence,group.id,group.clock.entryId ?? ''))return null;
 const t=binding.t,members=group.members;
 const participants=members.map(member=>({id:member.id,initial:member.private&&member.id!==group.you.memberId?'':memberName(member).trim().slice(0,1).toUpperCase(),colour:'var(--profile-art-blue)'}));
 const status=members.every(member=>member.status==='in_step')?'in-step':members.some(member=>member.status==='catching_up')?'catching-up':'waiting';
 const waiting=members.find(member=>member.status==='buffering');
 const act=(action:()=>Promise<unknown>)=>{try{const live=runtime.getSnapshot();if(!roomActionAllowed(runtime,binding.isCurrent(),live.fence,binding.getSnapshot().room,group.id,group.clock.entryId ?? ''))throw new DOMException('Room retired','AbortError');void action().catch(reportError);}catch(error){reportError(error);}};
 return <><span ref={anchor}><WatchTogetherPill name={group.name} participants={participants} count={members.length} status={status}
  waitingFor={waiting ? memberName(waiting) : undefined} t={t} open={open} onOpen={()=>{button.current=anchor.current?.querySelector('button')??null;setOpen(value=>!value);}}/></span>
  <Popover open={open} onOpenChange={setOpen} anchorRef={button} returnFocusRef={button} title={group.name} width={330}>
   <div className={styles.people}>{members.map(member=><div className={styles.person} key={member.id}>
    {member.private&&member.id!==group.you.memberId?<Icon name="i-person" size={26}/>:<ProfileFace initial={memberName(member).trim().slice(0,1).toUpperCase()} art={member.id===group.you.memberId?binding.profileArt:undefined} size={32}/>}
    <span>{memberName(member)}<small>{t(memberStatusKey(member))}</small></span>
   </div>)}<Button size="small" variant="glass" onClick={()=>act(()=>binding.service.leave())}>{t('web.together.leave')}</Button></div>
  </Popover></>;
}
