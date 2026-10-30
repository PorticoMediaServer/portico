import {useEffect} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {Dialog} from '../../../kit/overlays';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import type {WatchBindings} from '../types';
import {AudiobookListening} from './AudiobookListening';
import {ListeningPanels} from './ListeningPanels';
import {listeningSleepChoices} from './listening-owner';
import type {ListeningHost} from './use-listening-owner';
const t = defaultI18n.t;
export function AudiobookHost({binding, snapshot, expanded, phone, host, playOn}: {binding:WatchBindings;snapshot:WebPlaybackRuntimeSnapshot;expanded:boolean;phone:boolean;host?:ListeningHost;playOn?:()=>void}) {
  const owner=host?.owner,state=host?.state;
  useEffect(()=>{
    if(!owner||!state?.opening||!expanded)return;
    const key=(event:KeyboardEvent)=>{if(event.key==='Escape'&&!event.defaultPrevented&&!(event.target as Element|null)?.closest?.('[role="dialog"],input,textarea,select,[contenteditable="true"]')){event.preventDefault();owner.close(state);}};
    document.addEventListener('keydown',key);return()=>document.removeEventListener('keydown',key);
  },[owner,state,expanded]);
  const p=snapshot.presentation;
  if(!expanded||p?.kind!=='audiobook'||!owner||!state?.opening||snapshot.disposed||snapshot.dock.remote)return null;
  const identity=playbackIdentity(p.subject,'full',binding.environment),cover=binding.artwork.foreground(p.posterUrl??p.backdropUrl,600,1);
  const run=(work:()=>unknown)=>{try{void Promise.resolve(work()).catch(error=>{if(owner.canReport(state))binding.reportError(error);});}catch(error){if(owner.canReport(state))binding.reportError(error);}};
  const title=state.panel==='speed'?t('player.opt.speed'):state.panel==='sleep'?t('player.opt.sleepTimer'):state.panel==='queue'?t('next.music.upNext'):state.panel==='options'?t('player.more'):t('player.opt.chapters');
  const panels=<ListeningPanels host={host!} snapshot={snapshot} reportError={binding.reportError} artwork={binding.artwork}/>;
  return <>
    <AudiobookListening snapshot={snapshot} title={identity.title} subtitle={identity.subtitle.map((part,index)=><span key={`${part.id}:${index}`}>{part.text}</span>)} cover={cover} ambient={binding.artwork.ambient(p.posterUrl??p.backdropUrl)} chapters={state.chapters}
      speedChoices={owner.speeds()} sleepChoices={owner.sleepOffered()?listeningSleepChoices:[]} sleepMinutes={owner.sleepMinutes()} skips={binding.skips} phone={phone} canPlayOn={!!playOn}
      onIntent={(shown,intent)=>run(()=>owner.dispatch(state,shown,intent))} onSpeed={(shown,rate)=>run(()=>owner.setSpeed(state,shown,rate))}
      onSleep={(shown,minutes)=>run(()=>owner.setSleep(state,shown,minutes))} onChapter={(shown,row)=>run(()=>owner.chapter(state,shown,row))}
      onChapterPage={(shown,direction)=>run(()=>owner.chapterPage(state,shown,direction))} onChapterRetry={shown=>run(()=>owner.chapterPage(state,shown,'retry'))}
      onCollapse={()=>owner.close(state)} aside={panels}
      onShowPanel={owner.speeds().length||owner.sleepOffered()||owner.chaptersOffered()||playOn?(shown,panel)=>{if(panel==='play-on'){if(playOn)owner.external(state,shown,playOn);}else owner.show(state,panel,document.activeElement instanceof HTMLElement?document.activeElement:undefined);}:undefined}/>
    {phone&&<Dialog open={state.panelVisible} onOpenChange={open=>{if(!open)owner.closePanel(state);}} title={title} returnFocusRef={state.focus??undefined}>{panels}</Dialog>}
  </>;
}
