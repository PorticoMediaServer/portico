import {channelRequestID} from '../live-source-draft.ts';
import type {LibraryChannel} from '../library-channels.ts';
import {decodePostAdminLibraryChannelsIdLogoResponse} from '../../../contracts/src/decoders.ts';
import type {SettingsLibraryChannelsSource} from './channels-source';
export type ChannelLogoImage=Readonly<{name:string;size:number;type:string}>;
export type ChannelLogoUpload<Image extends ChannelLogoImage=File> = (channelId:string,file:Image,operationId:string,signal:AbortSignal)=>Promise<unknown>;
type Intent<Image extends ChannelLogoImage>=Readonly<{channel:LibraryChannel;file:Image;operationId:string}>;
export type ChannelLogoSnapshot=Readonly<{busy:boolean;pending?:Readonly<{channelId:string;name:string;fileName:string}>;error?:'image'|'write';done?:boolean}>;
const empty:ChannelLogoSnapshot=Object.freeze({busy:false});
const terminal=(error:unknown)=>{const e=(error&&typeof error==='object'?error:{}) as {status?:number;code?:string};return e.status===401||e.status===403||['unauthorized','forbidden','not_permitted'].includes(e.code??'');};

/** The owner composition supplies its fixed-route authenticated multipart
 * port. File is immutable; an uncertain retry keeps the exact bytes and ID. */
export class SettingsChannelLogoSource<Image extends ChannelLogoImage=File>{
  private state=empty;
  private readonly listeners=new Set<()=>void>();
  private readonly lifetime=new AbortController();
  private flight?:AbortController;
  private intent?:Intent<Image>;
  private readonly versions=new Map<string,number>();
  revision=(channelId:string)=>this.current()?this.versions.get(channelId):undefined;
  constructor(private readonly upload:ChannelLogoUpload<Image>,private readonly serverId:string,private readonly reader:SettingsLibraryChannelsSource,private readonly isCurrent:()=>boolean,private readonly reportError:(error:unknown)=>void,private readonly operationId:()=>string=channelRequestID){}
  private current(){return !this.lifetime.signal.aborted&&this.isCurrent();}
  getSnapshot=():ChannelLogoSnapshot=>this.current()?this.state:empty;
  subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
  private publish(patch:Partial<ChannelLogoSnapshot>){if(this.current()){this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}}
  choose=async(channel:LibraryChannel,file:Image):Promise<boolean>=>{
    if(!this.current()||this.flight||this.intent||!this.reader.getSnapshot().channels?.includes(channel))return false;
    if(file.size<=0||file.size>10*1024*1024||!['image/jpeg','image/png','image/webp'].includes(file.type)){this.publish({error:'image'});return false;}
    let intent:Intent<Image>;try{intent=Object.freeze({channel,file,operationId:this.operationId()});}catch{this.publish({error:'write'});return false;}
    if(!this.current()||this.flight||this.intent||!this.reader.getSnapshot().channels?.includes(channel))return false;
    this.intent=intent;return this.run(intent);
  };
  retry=async():Promise<boolean>=>{const intent=this.intent;return intent?this.run(intent):false;};
  private async run(intent:Intent<Image>):Promise<boolean>{
    if(!this.current()||this.flight||this.intent!==intent)return false;
    const flight=new AbortController();this.flight=flight;const owns=()=>this.current()&&this.flight===flight&&!flight.signal.aborted;
    this.publish({busy:true,pending:Object.freeze({channelId:intent.channel.config.id,name:intent.channel.config.name,fileName:intent.file.name}),error:undefined,done:undefined});let completed=false;
    try{
      if(!owns())return false;
      const response=decodePostAdminLibraryChannelsIdLogoResponse(await this.upload(intent.channel.config.id,intent.file,intent.operationId,flight.signal));
      if(!owns())return false;
      const result=response.result;
      if(response.serverId!==this.serverId||response.protocolVersion!=='1.0'||result.channelId!==intent.channel.config.id||result.width<16||result.width>4096||result.height<16||result.height>4096||result.revision<1||result.bytes<1||result.bytes>10*1024*1024||!['image/jpeg','image/png','image/webp'].includes(result.mediaType))throw new Error('invalid_channel_logo_receipt');
      this.versions.delete(result.channelId);this.versions.set(result.channelId,result.revision);if(this.versions.size>64)this.versions.delete(this.versions.keys().next().value!);
      this.intent=undefined;completed=true;this.publish({pending:undefined,done:true});
    }catch(error){if(owns()){
      if(terminal(error)){this.dispose();this.reportError(error);}
      else{const status=(error&&typeof error==='object'?error:{}) as {status?:number};if([400,413,415,422].includes(status.status??0)){this.intent=undefined;this.publish({pending:undefined});}this.publish({error:'write'});}
    }}finally{if(this.flight===flight){this.flight=undefined;this.publish({busy:false});}}
    if(completed&&this.current())await this.reader.load();return completed&&this.current();
  }
  dispose=()=>{this.flight?.abort();this.flight=undefined;this.intent=undefined;this.versions.clear();this.lifetime.abort();this.state=empty;for(const listener of this.listeners)listener();this.listeners.clear();};
}
