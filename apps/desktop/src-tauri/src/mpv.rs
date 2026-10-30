//! The C client API is the only native engine boundary. No shell or generic command is exported.
use crate::audio::{AudioFacts, AudioOutput};
use crate::boundary::{Command, Error, Observation, Result, Source};
use crate::equalizer::{EqualizerSettings, FILTER_LABEL};
use libloading::Library;
use std::ffi::{c_char, c_int, c_void, CStr, CString};
use std::path::Path;
use std::ptr;
use std::sync::{Arc, Mutex,atomic::{AtomicBool,AtomicI64,AtomicU64,AtomicUsize,Ordering}};

type Handle = *mut c_void;
type Create = unsafe extern "C" fn() -> Handle;
type Initialize = unsafe extern "C" fn(Handle) -> c_int;
type Destroy = unsafe extern "C" fn(Handle);
type Set = unsafe extern "C" fn(Handle, *const c_char, *const c_char) -> c_int;
type Cmd = unsafe extern "C" fn(Handle, *const *const c_char) -> c_int;
#[repr(C)] union NodeValue { string:*mut c_char,integer:i64,list:*mut NodeList }
#[repr(C)] struct Node { value:NodeValue,format:c_int }
#[repr(C)] struct NodeList { length:c_int,values:*mut Node,keys:*mut *mut c_char }
type CmdNode = unsafe extern "C" fn(Handle,*mut Node,*mut Node)->c_int;
type FreeNode = unsafe extern "C" fn(*mut Node);
type Get = unsafe extern "C" fn(Handle, *const c_char) -> *mut c_char;
type GetProperty = unsafe extern "C" fn(Handle,*const c_char,c_int,*mut c_void)->c_int;
type Free = unsafe extern "C" fn(*mut c_void);
type ApiVersion = unsafe extern "C" fn() -> u64;
#[repr(C)]struct Event{id:c_int,error:c_int,reply:u64,data:*mut c_void}
#[repr(C)]struct StartFile{playlist_entry_id:i64}
#[repr(C)]struct EndFile{reason:c_int,error:c_int,playlist_entry_id:i64}
type Wait=unsafe extern "C" fn(Handle,f64)->*const Event;
const LEVELING_FILTER:&str="portico-leveling";

pub struct Api {
    _library: Library,
    create: Create,
    initialize: Initialize,
    destroy: Destroy,
    option: Set,
    property: Set,
    command: Cmd,
    command_node:CmdNode,
    free_node:FreeNode,
    get: Get,
    get_property:GetProperty,
    free: Free,
    wait: Wait,
    pub version: u64,
}
impl Api {
    /// `path` comes from the native launcher/package, never from a web command.
    pub fn open(path: &Path) -> Result<Arc<Self>> {
        unsafe {
            let lib = Library::new(path).map_err(|_| Error::EngineUnavailable)?;
            macro_rules! symbol { ($name:literal, $t:ty) => { *lib.get::<$t>(concat!($name,"\0").as_bytes()).map_err(|_| Error::EngineUnavailable)? }; }
            let version = symbol!("mpv_client_api_version", ApiVersion)();
            if version >> 16 != 2 { return Err(Error::EngineUnavailable); }
            Ok(Arc::new(Self { create:symbol!("mpv_create",Create), initialize:symbol!("mpv_initialize",Initialize),
                destroy:symbol!("mpv_terminate_destroy",Destroy), option:symbol!("mpv_set_option_string",Set),
                property:symbol!("mpv_set_property_string",Set), command:symbol!("mpv_command",Cmd),command_node:symbol!("mpv_command_node",CmdNode),free_node:symbol!("mpv_free_node_contents",FreeNode),
                get:symbol!("mpv_get_property_string",Get),get_property:symbol!("mpv_get_property",GetProperty), free:symbol!("mpv_free",Free),wait:symbol!("mpv_wait_event",Wait), _library:lib,version }))
        }
    }
    pub unsafe fn symbol<T: Copy>(&self, name: &[u8]) -> Result<T> {
        unsafe { self._library.get::<T>(name).map(|x|*x).map_err(|_|Error::EngineUnavailable) }
    }
}

#[derive(Clone, Copy)]
pub enum VideoTarget { Headless, OpenGl, NativeView(usize) }
pub struct Mpv {
    pub api: Arc<Api>,
    handle: Handle,
    generation: Mutex<u64>,
    exclusive: bool,
    retired:AtomicBool,
    current_generation:AtomicU64,
    loaded:AtomicBool,
    surface_ready:AtomicBool,
    frame_drawn:AtomicU64,
    geometry_ready:AtomicBool,
    expected_url:Mutex<String>,
    failed:AtomicBool,
    events:Mutex<()>,
    newest_generation:AtomicU64,
    expected_entry:AtomicI64,
    started_entry:AtomicI64,
    restarted_generation:AtomicU64,
    native_target:bool,
    native_window:AtomicUsize,
    equalizer:Mutex<EqualizerSettings>,
    equalizer_applied:AtomicBool,
    equalizer_chain_known:AtomicBool,
    equalizer_route_pending:AtomicBool,
    starting:AtomicBool,
    pending_autoplay:AtomicBool,
    configured_spdif:String,
    leveling_gain:Mutex<f64>,
    leveling_applied:AtomicBool,
}
// libmpv explicitly permits client-API calls from any thread; the render context is separately
// owned by the GTK/main thread. The handle outlives every context through Arc<Mpv>.
unsafe impl Send for Mpv {}
unsafe impl Sync for Mpv {}
fn c(value: &str) -> Result<CString> { CString::new(value).map_err(|_|Error::InvalidCommand) }
fn status(value: c_int) -> Result<()> { if value < 0 { Err(Error::EngineFailure) } else { Ok(()) } }

impl Mpv {
    pub fn new(api: Arc<Api>, output: &AudioOutput, target: VideoTarget) -> Result<Arc<Self>> {
        let handle = unsafe { (api.create)() };
        if handle.is_null() { return Err(Error::EngineUnavailable); }
        let engine=Arc::new(Self {api,handle,generation:Mutex::new(0),exclusive:output.exclusive,retired:AtomicBool::new(false),current_generation:AtomicU64::new(0),loaded:AtomicBool::new(false),surface_ready:AtomicBool::new(false),frame_drawn:AtomicU64::new(0),geometry_ready:AtomicBool::new(false),expected_url:Mutex::new(String::new()),failed:AtomicBool::new(false),events:Mutex::new(()),newest_generation:AtomicU64::new(0),expected_entry:AtomicI64::new(0),started_entry:AtomicI64::new(0),restarted_generation:AtomicU64::new(0),native_target:matches!(target,VideoTarget::NativeView(_)),native_window:AtomicUsize::new(0),equalizer:Mutex::new(EqualizerSettings::default()),equalizer_applied:AtomicBool::new(false),equalizer_chain_known:AtomicBool::new(true),equalizer_route_pending:AtomicBool::new(false),starting:AtomicBool::new(false),pending_autoplay:AtomicBool::new(false),configured_spdif:output.passthrough.join(","),leveling_gain:Mutex::new(1.0),leveling_applied:AtomicBool::new(false)});
        engine.disable_script_engines()?;
        // The server owns subtitle rendering until the constrained native broker
        // exists. An original file's default track must not override "off".
        for (key,value) in [("config","no"),
            ("input-default-bindings","no"),("input-vo-keyboard","no"),("terminal","no"),("msg-level","all=no"),
            ("idle","yes"),("keep-open","yes"),("access-references","no"),("load-unsafe-playlists","no"),
            ("demuxer-lavf-o","protocol_whitelist=[http,https,tcp,tls,crypto]"),("hwdec","auto-safe"),
            ("audio-client-name","Portico"),("sid","no"),("sub-auto","no"),("audio-file-auto","no"),("autoload-files","no")] {
            engine.option(key,value)?;
        }
        for (key,value) in output.options(std::env::consts::OS)? { engine.option(key,&value)?; }
        match target {
            VideoTarget::Headless => {engine.option("vo","null")?;engine.option("ao","null")?;}
            VideoTarget::OpenGl => {engine.option("vo","libmpv")?;}
            VideoTarget::NativeView(parent) => {
                engine.option("vo","gpu-next")?;engine.option("wid",&parent.to_string())?;
                #[cfg(target_os="macos")]
                {engine.option("gpu-api","vulkan")?;engine.option("gpu-context","macvk")?;engine.option("target-colorspace-hint","yes")?;engine.option("border","no")?;engine.option("focus-on","never")?;engine.option("window-minimized","yes")?;engine.option("force-render","yes")?;engine.option("title","Portico")?;}
                #[cfg(target_os="windows")]
                {engine.option("gpu-api","d3d11")?;engine.option("gpu-context","d3d11")?;
                 engine.option("d3d11-output-mode","composition")?;engine.option("d3d11-output-csp","auto")?;}
            }
        }
        #[cfg(debug_assertions)]
        if std::env::var_os("PORTICO_SPIKE_MEDIA").is_some(){
            if let Some(report)=std::env::var_os("PORTICO_SPIKE_REPORT"){
                let path=std::path::PathBuf::from(report).with_extension("mpv.log");
                if let Some(path)=path.to_str(){engine.option("log-file",path)?;}
            }
        }
        let initialized=unsafe {(engine.api.initialize)(handle)};crate::spike::record("initialize",initialized);status(initialized)?;
        Ok(engine)
    }
    fn disable_script_engines(&self)->Result<()> {
        // mpv compiles these switches out with their corresponding script engines.
        // Only OPTION_NOT_FOUND means the forbidden feature itself is unavailable;
        // every other failure still closes initialization.
        for key in ["load-scripts","ytdl","osc","load-stats-overlay","load-console","load-auto-profiles","load-select","load-positioning","load-commands","load-context-menu"] {
            let code=unsafe{(self.api.option)(self.handle,c(key)?.as_ptr(),c("no")?.as_ptr())};
            if code != -5 {status(code)?;}
        }
        Ok(())
    }
    fn option(&self,name:&str,value:&str)->Result<()> {let code=unsafe{(self.api.option)(self.handle,c(name)?.as_ptr(),c(value)?.as_ptr())};if code<0{crate::spike::record(name,code);}status(code)}
    fn set(&self,name:&str,value:&str)->Result<()> {status(unsafe{(self.api.property)(self.handle,c(name)?.as_ptr(),c(value)?.as_ptr())})}
    fn command(&self,args:&[&str])->Result<()> {
        let values=args.iter().map(|x|c(x)).collect::<Result<Vec<_>>>()?;
        let mut pointers=values.iter().map(|x|x.as_ptr()).collect::<Vec<_>>();pointers.push(ptr::null());
        status(unsafe{(self.api.command)(self.handle,pointers.as_ptr())})
    }
    fn load_node(&self,path:&str,options:&[(&str,String)])->Result<()> {
        let keys=options.iter().map(|(key,_)|c(key)).collect::<Result<Vec<_>>>()?;
        let values=options.iter().map(|(_,value)|c(value)).collect::<Result<Vec<_>>>()?;
        let mut option_keys=keys.iter().map(|s|s.as_ptr().cast_mut()).collect::<Vec<_>>();
        let mut option_values=values.iter().map(|s|Node{value:NodeValue{string:s.as_ptr().cast_mut()},format:1}).collect::<Vec<_>>();
        let mut options=NodeList{length:keys.len() as c_int,values:option_values.as_mut_ptr(),keys:option_keys.as_mut_ptr()};
        let strings=[c("loadfile")?,c(path)?,c("replace")?];
        let mut args=strings.iter().map(|s|Node{value:NodeValue{string:s.as_ptr().cast_mut()},format:1}).collect::<Vec<_>>();
        args.push(Node{value:NodeValue{integer:-1},format:4});args.push(Node{value:NodeValue{list:&mut options},format:8});
        let mut list=NodeList{length:args.len() as c_int,values:args.as_mut_ptr(),keys:ptr::null_mut()};
        let mut command=Node{value:NodeValue{list:&mut list},format:7};
        let mut reply=Node{value:NodeValue{integer:0},format:0};
        let code=unsafe{(self.api.command_node)(self.handle,&mut command,&mut reply)};
        let mut entry=0;
        unsafe{
            if code>=0&&reply.format==8&&!reply.value.list.is_null(){
                let map=&*reply.value.list;
                if (0..=64).contains(&map.length)&&!map.keys.is_null()&&!map.values.is_null(){
                    for index in 0..map.length as usize{
                        let key=*map.keys.add(index);let value=&*map.values.add(index);
                        if !key.is_null()&&CStr::from_ptr(key)==c"playlist_entry_id"&&value.format==4{entry=value.value.integer;}
                    }
                }
            }
            (self.api.free_node)(&mut reply);
        }
        status(code)?;if entry<=0{return Err(Error::EngineFailure)}
        self.expected_entry.store(entry,Ordering::Release);Ok(())
    }
    fn clear_file_facts(&self){
        self.loaded.store(false,Ordering::Release);self.frame_drawn.store(0,Ordering::Release);
        self.expected_entry.store(0,Ordering::Release);self.started_entry.store(0,Ordering::Release);
        self.restarted_generation.store(0,Ordering::Release);self.native_window.store(0,Ordering::Release);
    }
    pub fn property(&self,name:&str)->Option<String> {
        let key=c(name).ok()?;
        unsafe {let p=(self.api.get)(self.handle,key.as_ptr());if p.is_null(){return None;}
            let value=CStr::from_ptr(p).to_string_lossy().into_owned();(self.api.free)(p.cast());Some(value)}
    }
    fn number(&self,name:&str)->Option<f64> {self.property(name)?.parse().ok().filter(|x:&f64|x.is_finite())}
    /// Actual decoder inventory, not a hardware/output or per-resource decode test.
    /// mpv.io/manual/stable/#property-list: decoder-list's MPV_FORMAT_NODE maps.
    pub fn decoder_inventory(&self)->Result<Vec<String>>{
        let _transport=self.generation.lock().map_err(|_|Error::EngineFailure)?;
        if self.retired.load(Ordering::Acquire){return Err(Error::EngineUnavailable)}
        let mut node=Node{value:NodeValue{integer:0},format:0};
        status(unsafe{(self.api.get_property)(self.handle,c"decoder-list".as_ptr(),6,(&mut node as *mut Node).cast())})?;
        let result=(||unsafe{
            if node.format!=7||node.value.list.is_null(){return Err(Error::EngineFailure)}
            let list=&*node.value.list;
            if !(1..=4096).contains(&list.length)||list.values.is_null(){return Err(Error::EngineFailure)}
            let mut codecs=std::collections::BTreeSet::new();
            for index in 0..list.length as usize{
                let entry=&*list.values.add(index);
                if entry.format!=8||entry.value.list.is_null(){return Err(Error::EngineFailure)}
                let map=&*entry.value.list;
                if !(1..=16).contains(&map.length)||map.values.is_null()||map.keys.is_null(){return Err(Error::EngineFailure)}
                for field in 0..map.length as usize{
                    let key=*map.keys.add(field);let value=&*map.values.add(field);
                    if key.is_null()||CStr::from_ptr(key)!=c"codec"{continue}
                    if value.format!=1||value.value.string.is_null(){return Err(Error::EngineFailure)}
                    let codec=CStr::from_ptr(value.value.string).to_str().map_err(|_|Error::EngineFailure)?;
                    if codec.is_empty()||codec.len()>64||!codec.bytes().all(|c|c.is_ascii_lowercase()||c.is_ascii_digit()||matches!(c,b'_'|b'-'|b'.'|b'+')){return Err(Error::EngineFailure)}
                    codecs.insert(codec.to_owned());
                }
            }
            Ok(codecs.into_iter().collect())
        })();
        unsafe{(self.api.free_node)(&mut node)};result
    }
    fn flag(&self,name:&str)->bool {self.property(name).as_deref()==Some("yes")}
    pub fn handle(&self)->Handle {self.handle}
    /// App-wide DSP. No output identity participates in lookup or persistence. The labelled
    /// setting is shared by music/video and every physical route. Passthrough stays untouched.
    pub fn apply_equalizer(&self,next:&EqualizerSettings)->Result<()> {
        next.validate()?;
        let _transport=self.generation.lock().map_err(|_|Error::EngineFailure)?;
        if self.retired.load(Ordering::Acquire){return Err(Error::EngineUnavailable)}
        let mut previous=self.equalizer.lock().map_err(|_|Error::EngineFailure)?;
        let was_paused=self.flag("pause");let was_failed=self.failed.load(Ordering::Acquire);
        // A failed rollback leaves an unknown DSP chain. Keep playback fenced until the
        // engine is recreated from the persisted setting rather than guessing its state.
        if !self.equalizer_chain_known.load(Ordering::Acquire){return Err(Error::EngineFailure);}
        if self.configure_equalizer(next).is_err(){
            let restored=self.configure_equalizer(&previous).is_ok();
            self.equalizer_chain_known.store(restored,Ordering::Release);
            if restored{
                self.failed.store(was_failed,Ordering::Release);
                let _=self.set("pause",if was_paused{"yes"}else{"no"});
            }else{self.failed.store(true,Ordering::Release);let _=self.set("pause","yes");}
            return Err(Error::EngineFailure);
        }
        *previous=next.clone();Ok(())
    }
    fn clear_equalizer(&self)->Result<()> {
        self.equalizer_applied.store(false,Ordering::Release);
        let result=(||{
            let marker=format!("@{FILTER_LABEL}:");
            // mpv0.41's fixed af string representation prints each label as @name:.
            // Only native-owned filters enter this chain; never parse a web filter string.
            if self.property("af").ok_or(Error::EngineFailure)?.contains(&marker){
                self.command(&["af","remove",&format!("@{FILTER_LABEL}")])?;
            }
            if self.property("af").ok_or(Error::EngineFailure)?.contains(&marker){return Err(Error::EngineFailure);}
            Ok(())
        })();
        self.equalizer_chain_known.store(result.is_ok(),Ordering::Release);
        if result.is_err(){let _=self.set("pause","yes");}
        result
    }
    /// Called under the transport lock. true means route negotiation is ready. A filter
    /// failure is silent clean bypass for this song; only failure to clear an unknown chain
    /// is a real playback error. The persistent setting is never silently switched off.
    fn configure_equalizer(&self,next:&EqualizerSettings)->Result<bool> {
        self.equalizer_route_pending.store(false,Ordering::Release);
        if !next.enabled||!self.loaded.load(Ordering::Acquire){self.clear_equalizer()?;return Ok(true);}
        let format=self.property("audio-out-params/format");
        if format.as_deref().is_some_and(|format|format.starts_with("spdif")){
            self.clear_equalizer()?;return Ok(true);
        }
        if !self.configured_spdif.is_empty()&&format.is_none()&&self.property("aid").as_deref()!=Some("no"){
            self.clear_equalizer()?;self.equalizer_route_pending.store(true,Ordering::Release);return Ok(false);
        }
        let filter=next.filter()?.ok_or(Error::InvalidCommand)?;
        self.equalizer_applied.store(false,Ordering::Release);
        if self.command(&["af","add",&filter]).is_err(){
            self.clear_equalizer()?;
            eprintln!("equalizer_filter_unavailable");
            return Ok(true);
        }
        self.equalizer_applied.store(true,Ordering::Release);Ok(true)
    }
    fn actual_equalizer_applied(&self)->bool {
        self.equalizer_applied.load(Ordering::Acquire)&&self.loaded.load(Ordering::Acquire)
            &&!self.starting.load(Ordering::Acquire)&&!self.failed.load(Ordering::Acquire)
            &&!self.flag("eof-reached")&&!self.flag("idle-active")
            &&self.number("audio-out-params/samplerate").is_some_and(|rate|rate>0.0)
            &&self.property("audio-out-params/format").is_some_and(|format|!format.starts_with("spdif"))
            &&self.property("af").is_some_and(|chain|chain.contains(&format!("@{FILTER_LABEL}:")))
    }
    fn clear_leveling(&self)->Result<()> {
        self.leveling_applied.store(false,Ordering::Release);
        let result=(||{
            let marker=format!("@{LEVELING_FILTER}:");
            if self.property("af").ok_or(Error::EngineFailure)?.contains(&marker){self.command(&["af","remove",&format!("@{LEVELING_FILTER}")])?;}
            if self.property("af").ok_or(Error::EngineFailure)?.contains(&marker){return Err(Error::EngineFailure)}
            Ok(())
        })();
        if result.is_err(){self.equalizer_chain_known.store(false,Ordering::Release);self.failed.store(true,Ordering::Release);let _=self.set("pause","yes");}
        result
    }
    fn configure_leveling(&self,gain:f64)->Result<bool> {
        self.clear_leveling()?;
        if gain==1.0||!self.loaded.load(Ordering::Acquire){return Ok(true)}
        let format=self.property("audio-out-params/format");
        if format.as_deref().is_some_and(|format|format.starts_with("spdif")){return Ok(true)}
        if !self.configured_spdif.is_empty()&&format.is_none()&&self.property("aid").as_deref()!=Some("no"){
            self.equalizer_route_pending.store(true,Ordering::Release);return Ok(false)
        }
        // A fixed labelled graph from one finite bounded scalar, never a web filter string.
        let filter=format!("@{LEVELING_FILTER}:lavfi=[volume=volume={gain}:precision=double]");
        if self.command(&["af","add",&filter]).is_err(){self.clear_leveling()?;return Ok(true)}
        self.leveling_applied.store(true,Ordering::Release);Ok(true)
    }
    fn actual_leveling_applied(&self)->bool {
        self.leveling_applied.load(Ordering::Acquire)&&self.loaded.load(Ordering::Acquire)
            &&!self.starting.load(Ordering::Acquire)&&!self.failed.load(Ordering::Acquire)
            &&!self.flag("eof-reached")&&!self.flag("idle-active")
            &&self.number("audio-out-params/samplerate").is_some_and(|rate|rate>0.0)
            &&self.property("audio-out-params/format").is_some_and(|format|!format.starts_with("spdif"))
            &&self.property("af").is_some_and(|chain|chain.contains(&format!("@{LEVELING_FILTER}:")))
    }
    fn finish_start(&self)->Result<()> {
        if !self.loaded.load(Ordering::Acquire)||!(self.starting.load(Ordering::Acquire)||self.equalizer_route_pending.load(Ordering::Acquire)){return Ok(());}
        let settings=self.equalizer.lock().map_err(|_|Error::EngineFailure)?;
        let gain=*self.leveling_gain.lock().map_err(|_|Error::EngineFailure)?;
        if self.configure_equalizer(&settings)?&&self.configure_leveling(gain)?&&self.starting.swap(false,Ordering::AcqRel){
            self.set("pause",if self.pending_autoplay.load(Ordering::Acquire){"no"}else{"yes"})?;
        }
        Ok(())
    }
    pub fn equalizer_matches(&self,settings:&EqualizerSettings)->bool {
        let Ok(_transport)=self.generation.lock()else{return false;};
        self.equalizer.lock().map(|current|self.actual_equalizer_applied()&&*current==*settings).unwrap_or(false)
    }
    pub fn execute(&self,command:Command)->Result<()> {
        // Hold generation through the native command; stale async work can never affect the next source.
        let mut generation=self.generation.lock().map_err(|_|Error::EngineFailure)?;
        if self.retired.load(Ordering::Acquire){return Err(Error::EngineUnavailable)}
        match command {
            Command::Load{source} => {
                if !self.equalizer_chain_known.load(Ordering::Acquire){return Err(Error::EngineFailure);}
                source.validate()?;
                if source.generation<=self.newest_generation.load(Ordering::Acquire){return Err(Error::StaleGeneration);}
                self.loaded.store(false,Ordering::Release);
                self.newest_generation.store(source.generation,Ordering::Release);
                self.drain();self.clear_file_facts();self.failed.store(false,Ordering::Release);
                // Negotiate every new track without the previous track's EQ. This preserves
                // passthrough; only an observed PCM route receives the app's EQ afterwards.
                self.clear_equalizer()?;self.clear_leveling()?;*self.leveling_gain.lock().map_err(|_|Error::EngineFailure)?=1.0;self.equalizer_route_pending.store(false,Ordering::Release);
                *self.expected_url.lock().map_err(|_|Error::EngineFailure)?=source.url.clone();
                self.load(&source)?;*generation=source.generation;self.current_generation.store(*generation,Ordering::Release);Ok(())
            }
            other => {
                let g=match &other {Command::Play{generation}|Command::Pause{generation}|Command::Seek{generation,..}|Command::Rate{generation,..}|Command::FrameStep{generation,..}|Command::Processing{generation,..}|Command::Volume{generation,..}|Command::Stop{generation}=>*generation,_=>unreachable!()};
                if *generation==0 || *generation!=g {return Err(Error::StaleGeneration);}
                match other {
                    Command::Play{..} if self.equalizer_chain_known.load(Ordering::Acquire)=>{
                        self.pending_autoplay.store(true,Ordering::Release);
                        if self.starting.load(Ordering::Acquire){Ok(())}else{self.set("pause","no")}
                    },
                    Command::Play{..}=>Err(Error::EngineFailure),Command::Pause{..}=>{self.pending_autoplay.store(false,Ordering::Release);self.set("pause","yes")},
                    Command::Seek{milliseconds,..} if milliseconds.is_finite()&&(0.0..=1e12).contains(&milliseconds)=>self.command(&["seek",&(milliseconds/1000.0).to_string(),"absolute+exact"]),
                    Command::FrameStep{direction,..} if matches!(direction,-1|1)=>{
                        self.drain();
                        if !self.loaded.load(Ordering::Acquire)||self.starting.load(Ordering::Acquire)
                            ||self.restarted_generation.load(Ordering::Acquire)!=g
                            ||self.failed.load(Ordering::Acquire)||!self.flag("pause")||self.flag("paused-for-cache")
                            ||self.flag("eof-reached")||self.number("width").unwrap_or(0.0)<=0.0{return Err(Error::InvalidCommand)}
                        self.command(&[if direction==1{"frame-step"}else{"frame-back-step"}])
                    },
                    Command::Rate{rate,..} if rate.is_finite()&&(0.25..=4.0).contains(&rate)=>self.set("speed",&rate.to_string()),
                    Command::Volume{volume,muted,..} if volume.is_finite()&&(0.0..=1.0).contains(&volume)=>{self.set("volume",&(volume*100.0).to_string())?;self.set("mute",if muted{"yes"}else{"no"})},
                    Command::Processing{gain,..}=>{
                        if !gain.is_finite()||!(0.000001..=32.0).contains(&gain){return Err(Error::InvalidCommand)}
                        let mut stored=self.leveling_gain.lock().map_err(|_|Error::EngineFailure)?;
                        let was_paused=self.flag("pause");let was_failed=self.failed.load(Ordering::Acquire);
                        if self.configure_leveling(gain).is_err(){
                            let restored=self.configure_leveling(*stored).is_ok();self.equalizer_chain_known.store(restored,Ordering::Release);
                            if restored{self.failed.store(was_failed,Ordering::Release);let _=self.set("pause",if was_paused{"yes"}else{"no"});}
                            else{self.failed.store(true,Ordering::Release);let _=self.set("pause","yes");}
                            return Err(Error::EngineFailure)
                        }
                        *stored=gain;Ok(())
                    },
                    Command::Stop{..}=>{self.command(&["stop"])?;*generation=0;self.current_generation.store(0,Ordering::Release);self.clear_file_facts();self.starting.store(false,Ordering::Release);self.equalizer_applied.store(false,Ordering::Release);self.equalizer_route_pending.store(false,Ordering::Release);Ok(())},
                    _=>Err(Error::InvalidCommand)
                }
            }
        }
    }
    fn load(&self,source:&Source)->Result<()> {
        self.pending_autoplay.store(source.autoplay,Ordering::Release);self.starting.store(true,Ordering::Release);
        self.set("pause","yes")?;
        self.load_node(&source.url,&[("start",(source.start_position_ms/1000.0).to_string()),("sid","no".into()),("demuxer-lavf-o","protocol_whitelist=[http,https,tcp,tls,crypto],format_whitelist=[aac,ac3,aiff,ape,asf,avi,caf,dff,dsf,dts,eac3,flac,flv,matroska,webm,mov,mp3,mpeg,mpegts,ogg,wav,wv]".into())])
    }
    /// Only the native download store can resolve this path. Fixed per-file options keep
    /// local demuxers from following a playlist/network reference and do not alter the
    /// next remote source's protocol allowlist. No option map is accepted from IPC.
    pub(crate) fn load_local(&self,path:&Path,next_generation:u64,start_ms:f64,autoplay:bool)->Result<()> {
        if next_generation==0 || next_generation>9_007_199_254_740_991 || !start_ms.is_finite() || !(0.0..=1e12).contains(&start_ms) || !path.is_absolute(){return Err(Error::InvalidSource)}
        let mut generation=self.generation.lock().map_err(|_|Error::EngineFailure)?;
        if self.retired.load(Ordering::Acquire){return Err(Error::EngineUnavailable)}
        if !self.equalizer_chain_known.load(Ordering::Acquire){return Err(Error::EngineFailure)}
        if next_generation<=self.newest_generation.load(Ordering::Acquire){return Err(Error::StaleGeneration)}
        self.loaded.store(false,Ordering::Release);
        self.newest_generation.store(next_generation,Ordering::Release);
        self.drain();self.clear_file_facts();self.failed.store(false,Ordering::Release);
        self.clear_equalizer()?;self.clear_leveling()?;*self.leveling_gain.lock().map_err(|_|Error::EngineFailure)?=1.0;self.equalizer_route_pending.store(false,Ordering::Release);
        *self.expected_url.lock().map_err(|_|Error::EngineFailure)?=path.to_str().ok_or(Error::InvalidSource)?.into();
        self.pending_autoplay.store(autoplay,Ordering::Release);self.starting.store(true,Ordering::Release);self.set("pause","yes")?;
        self.load_node(path.to_str().ok_or(Error::InvalidSource)?,&[("start",(start_ms/1000.0).to_string()),("sid","no".into()),("demuxer-lavf-o","protocol_whitelist=[file],format_whitelist=[aac,ac3,aiff,ape,asf,avi,caf,dff,dsf,dts,eac3,flac,flv,matroska,webm,mov,mp3,mpeg,mpegts,ogg,wav,wv]".into()),("access-references","no".into()),("load-unsafe-playlists","no".into())])?;
        *generation=next_generation;self.current_generation.store(*generation,Ordering::Release);Ok(())
    }
    #[cfg(debug_assertions)]
    pub fn load_spike_fixture(&self,path:&Path)->Result<()> {
        if !path.is_absolute() || !path.is_file(){return Err(Error::InvalidSource);}
        self.load_local(path,1,0.0,true)?;
        self.set("loop-file","inf")?;
        Ok(())
    }
    pub fn observation(&self)->Observation {
        let transport=self.generation.lock().unwrap_or_else(|x|x.into_inner());let generation=*transport;
        self.drain();
        if self.finish_start().is_err(){self.equalizer_chain_known.store(false,Ordering::Release);self.failed.store(true,Ordering::Release);let _=self.set("pause","yes");}
        #[cfg(target_os="macos")]
        if self.native_target&&unsafe{libc::pthread_main_np()}==0 {
            let window=self.property("window-id").and_then(|v|v.parse::<usize>().ok()).unwrap_or(0);
            self.native_window.store(window,Ordering::Release);
            if self.loaded.load(Ordering::Acquire)&&self.restarted_generation.load(Ordering::Acquire)==generation&&self.property("video-frame-info/interlaced").is_some(){self.frame_drawn(generation);}
        }
        let state=if generation==0{"idle"}else if self.failed.load(Ordering::Acquire){"error"}else if self.starting.load(Ordering::Acquire)||!self.loaded.load(Ordering::Acquire){"loading"}else if self.flag("eof-reached"){"ended"}else if self.flag("idle-active"){"loading"}
            else if self.flag("paused-for-cache"){"buffering"}else if self.flag("pause"){"paused"}else{"playing"};
        // A load command changes the generation before mpv has retired the previous
        // file. Its time-pos/eof properties belong to that old file until FILE_LOADED.
        let current_file=self.loaded.load(Ordering::Acquire)&&!matches!(state,"idle"|"loading"|"error");
        let output_ready=matches!(state,"playing"|"paused"|"buffering")&&current_file
            &&self.restarted_generation.load(Ordering::Acquire)==generation
            &&self.expected_entry.load(Ordering::Acquire)>0
            &&self.started_entry.load(Ordering::Acquire)==self.expected_entry.load(Ordering::Acquire);
        let observed_playback=if output_ready{
            crate::boundary::RendererOutput::pcm(self.property("audio-out-params/format").as_deref(),self.number("audio-out-params/samplerate"),self.number("audio-out-params/channel-count"))
        }else{crate::boundary::RendererOutput::default()};
        Observation{generation,state:state.into(),position_ms:if current_file{self.number("time-pos").unwrap_or(0.0).max(0.0)*1000.0}else{0.0},
            observed_playback,
            rate:self.number("speed").unwrap_or(1.0),volume:self.number("volume").unwrap_or(100.0)/100.0,
            muted:self.flag("mute"),dropped_frames:self.number("frame-drop-count").unwrap_or(0.0) as u64,
            native_surface_active:!matches!(state,"idle"|"loading"|"ended"|"error")&&self.surface_ready.load(Ordering::Acquire)&&self.geometry_ready.load(Ordering::Acquire)&&self.frame_drawn.load(Ordering::Acquire)==generation&&self.loaded.load(Ordering::Acquire)&&self.number("width").unwrap_or(0.0)>0.0,
            eq_applied:!matches!(state,"idle"|"loading"|"ended"|"error")&&self.actual_equalizer_applied(),leveling_applied:!matches!(state,"idle"|"loading"|"ended"|"error")&&self.actual_leveling_applied()}
    }
    fn drain(&self){
        let _lock=self.events.lock().unwrap_or_else(|e|e.into_inner());
        for _ in 0..256{unsafe{
            let event=(self.api.wait)(self.handle,0.0);if event.is_null()||(*event).id==0{break;}
            if (*event).id==6&&!(*event).data.is_null(){self.started_entry.store((*((*event).data as *const StartFile)).playlist_entry_id,Ordering::Release);self.loaded.store(false,Ordering::Release);self.frame_drawn.store(0,Ordering::Release);self.restarted_generation.store(0,Ordering::Release);}
            let same_entry=self.expected_entry.load(Ordering::Acquire)>0&&self.started_entry.load(Ordering::Acquire)==self.expected_entry.load(Ordering::Acquire);
            if (*event).id==8 {let same=same_entry&&self.expected_url.lock().map(|u|self.property("path").as_deref()==Some(u.as_str())).unwrap_or(false);self.loaded.store(same,Ordering::Release);}
            if (*event).id==21&&same_entry&&self.loaded.load(Ordering::Acquire){self.restarted_generation.store(self.newest_generation.load(Ordering::Acquire),Ordering::Release);}
            if (*event).id==7&&!(*event).data.is_null(){let fields=&*((*event).data as *const EndFile);if fields.playlist_entry_id==self.expected_entry.load(Ordering::Acquire)&&fields.reason==4{self.failed.store(true,Ordering::Release);}}
        }}
    }
    /// Close the transport before detaching its renderer. Queued mutations can no
    /// longer reopen it; the render thread reads only atomics during retirement.
    pub(crate) fn retire(&self)->Result<()> {
        let mut generation=self.generation.lock().map_err(|_|Error::EngineFailure)?;
        self.retired.store(true,Ordering::Release);
        self.current_generation.store(0,Ordering::Release);
        let result=self.command(&["stop"]);
        *generation=0;self.clear_file_facts();self.set_surface_ready(false);
        self.set_geometry_ready(false);self.equalizer_applied.store(false,Ordering::Release);self.leveling_applied.store(false,Ordering::Release);
        result
    }
    pub fn set_surface_ready(&self,value:bool){self.surface_ready.store(value,Ordering::Release);if !value{self.frame_drawn.store(0,Ordering::Release);}}
    pub fn set_geometry_ready(&self,value:bool){self.geometry_ready.store(value,Ordering::Release);}
    pub fn matches_generation(&self,generation:u64)->bool{generation>0&&!self.retired.load(Ordering::Acquire)&&self.current_generation.load(Ordering::Acquire)==generation}
    pub fn render_generation(&self)->u64{let generation=self.newest_generation.load(Ordering::Acquire);if self.loaded.load(Ordering::Acquire){generation}else{0}}
    pub fn frame_drawn(&self,generation:u64){if generation>0{self.frame_drawn.store(generation,Ordering::Release);}}
    #[cfg(target_os="macos")]
    pub(crate) fn cached_native_window(&self)->usize{self.native_window.load(Ordering::Acquire)}
    pub fn audio_facts(&self)->AudioFacts {
        let transport=self.generation.lock().unwrap_or_else(|x|x.into_inner());
        let current=self.matches_generation(*transport)&&self.loaded.load(Ordering::Acquire)
            &&!self.starting.load(Ordering::Acquire)&&!self.failed.load(Ordering::Acquire)
            &&!self.flag("eof-reached")&&!self.flag("idle-active");
        let positive=|name:&str|if current{self.number(name).filter(|n|*n>0.0&&*n<=9_007_199_254_740_991.0).map(|n|n as u64)}else{None};
        let (equalizer_enabled,eq_applied)=self.equalizer.lock().map(|settings|(settings.enabled,self.actual_equalizer_applied())).unwrap_or((false,false));
        AudioFacts{generation:*transport,requested_exclusive:self.exclusive,source_rate:positive("audio-params/samplerate"),
            output_rate:positive("audio-out-params/samplerate"),output_channels:positive("audio-out-params/channel-count"),
            output_format:if current{self.property("audio-out-params/format").filter(|format|!format.is_empty())}else{None},bit_exact_capture_verified:false,
            equalizer_enabled,eq_applied,leveling_applied:self.actual_leveling_applied(),
            bit_perfect_excluded_by_equalizer:equalizer_enabled||!self.equalizer_chain_known.load(Ordering::Acquire)}
    }
    /// Native proof runner only: output is an owned CLI-selected scratch file. Not registered IPC.
    pub fn capture_pcm(api:Arc<Api>,input:&Path,output:&Path,rate:u32,channels:u8)->Result<()> {
        Self::capture_playlist(api,&[input],output,rate,channels)
    }
    pub fn capture_playlist(api:Arc<Api>,inputs:&[&Path],output:&Path,rate:u32,channels:u8)->Result<()> {
        if inputs.is_empty() || inputs.len()>32{return Err(Error::InvalidCommand);}
        let handle=unsafe{(api.create)()};if handle.is_null(){return Err(Error::EngineUnavailable);}
        let e=Self{api,handle,generation:Mutex::new(1),exclusive:false,retired:AtomicBool::new(false),current_generation:AtomicU64::new(1),loaded:AtomicBool::new(false),surface_ready:AtomicBool::new(false),frame_drawn:AtomicU64::new(0),geometry_ready:AtomicBool::new(false),expected_url:Mutex::new(String::new()),failed:AtomicBool::new(false),events:Mutex::new(()),newest_generation:AtomicU64::new(0),expected_entry:AtomicI64::new(0),started_entry:AtomicI64::new(0),restarted_generation:AtomicU64::new(0),native_target:false,native_window:AtomicUsize::new(0),equalizer:Mutex::new(EqualizerSettings::default()),equalizer_applied:AtomicBool::new(false),equalizer_chain_known:AtomicBool::new(true),equalizer_route_pending:AtomicBool::new(false),starting:AtomicBool::new(false),pending_autoplay:AtomicBool::new(false),configured_spdif:String::new(),leveling_gain:Mutex::new(1.0),leveling_applied:AtomicBool::new(false)};
        e.disable_script_engines()?;
        for (k,v) in [("config","no"),("terminal","no"),("msg-level","all=no"),
            ("vo","null"),("ao","pcm"),("audio-format","s32"),("audio-display","no"),("gapless-audio","yes"),("replaygain","no"),("volume","100"),("pause","yes")] {e.option(k,v)?;}
        e.option("ao-pcm-file",output.to_str().ok_or(Error::InvalidCommand)?)?;e.option("ao-pcm-waveheader","no")?;
        e.option("audio-samplerate",&rate.to_string())?;e.option("audio-channels",&channels.to_string())?;
        status(unsafe{(e.api.initialize)(handle)})?;
        for (i,input) in inputs.iter().enumerate(){e.command(&["loadfile",input.to_str().ok_or(Error::InvalidCommand)?,if i==0{"replace"}else{"append"}])?;}
        e.set("pause","no")?;
        let start=std::time::Instant::now();
        while start.elapsed().as_secs()<30 {
            if (e.flag("idle-active")&&start.elapsed().as_millis()>1000)
                || e.flag("eof-reached")&&e.number("playlist-pos")==Some((inputs.len()-1) as f64) {return Ok(());}
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        Err(Error::EngineFailure)
    }
}
impl Drop for Mpv {fn drop(&mut self){unsafe{(self.api.destroy)(self.handle)}}}

#[cfg(test)]
mod pcm_original_tests {
    use super::*;
    #[test]
    #[ignore="explicit native libmpv and owned WAV/AIFF; no physical or bit-perfect claim"]
    fn actual_pcm_integer_and_float_inventory_and_original_containers(){
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library required");
        let root=std::path::PathBuf::from(std::env::var_os("PORTICO_TEST_PCM_FIXTURE").expect("owned fixture required"));
        let root=root.parent().unwrap();
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        let inventory=engine.decoder_inventory().unwrap();
        for codec in ["pcm_u8","pcm_s16le","pcm_s16be","pcm_s24le","pcm_s24be","pcm_s32le","pcm_s32be","pcm_f32le","pcm_f32be","pcm_f64le","pcm_f64be"]{
            assert!(inventory.iter().any(|value|value==codec),"required decoder missing: {codec}");
        }
        for (generation,name,codec) in [(1,"owned-24bit.wav","pcm_s24le"),(2,"owned-24bit.aiff","pcm_s24be")]{
            engine.load_local(&root.join(name),generation,0.0,true).unwrap();
            let deadline=std::time::Instant::now()+std::time::Duration::from_secs(5);
            loop {
                let observed=engine.observation();assert_ne!(observed.state,"error");
                if observed.position_ms>10.0{break}
                assert!(std::time::Instant::now()<deadline,"original PCM did not advance");
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            assert_eq!(engine.property("audio-codec-name").as_deref(),Some(codec));
            assert_eq!(engine.audio_facts().source_rate,Some(48000));
        }
        engine.retire().unwrap();
    }
}

#[cfg(test)]
mod wavpack_tests {
    use super::*;
    #[test]
    #[ignore="explicit native libmpv and runner-generated WavPack; no physical output claim"]
    fn actual_wavpack_decoder_plays_the_owned_original_container(){
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library required");
        let fixture=std::env::var_os("PORTICO_TEST_WAVPACK_FIXTURE").expect("owned WavPack fixture required");
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        assert!(engine.decoder_inventory().unwrap().iter().any(|codec|codec=="wavpack"));
        engine.load_local(Path::new(&fixture),1,0.0,true).unwrap();
        let deadline=std::time::Instant::now()+std::time::Duration::from_secs(5);
        loop {
            let observed=engine.observation();assert_ne!(observed.state,"error");
            if observed.position_ms>10.0{break}
            assert!(std::time::Instant::now()<deadline,"original WavPack did not advance");
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        assert_eq!(engine.property("audio-codec-name").as_deref(),Some("wavpack"));
        assert_eq!(engine.audio_facts().source_rate,Some(48000));
        engine.retire().unwrap();assert!(!engine.matches_generation(1));
    }
}

#[cfg(test)]
mod local_source_tests {
    use super::*;
    #[test]
    #[ignore="explicit native libmpv and owned default-subtitle fixture; no GUI or screenshot"]
    fn unselected_default_embedded_subtitle_stays_off(){
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library required");
        let fixture=std::env::var_os("PORTICO_TEST_SUBTITLE_FIXTURE").expect("owned subtitle fixture required");
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        for generation in [1,2]{
            engine.load_local(Path::new(&fixture),generation,0.0,true).unwrap();
            let deadline=std::time::Instant::now()+std::time::Duration::from_secs(5);
            loop {
                let observed=engine.observation();assert_ne!(observed.state,"error");
                if observed.position_ms>10.0{break}
                assert!(std::time::Instant::now()<deadline,"owned default-track movie did not advance");
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
            assert_eq!(engine.property("track-list/1/type").as_deref(),Some("sub"));
            assert_eq!(engine.property("track-list/1/default").as_deref(),Some("yes"));
            assert_eq!(engine.property("sid").as_deref(),Some("no"));
            // Simulate a previous explicit native choice; the next source owns
            // a fresh decision and must not inherit it.
            if generation==1{engine.set("sid","auto").unwrap();}
        }
        engine.retire().unwrap();
    }
    #[test]
    #[ignore="requires an explicit native libmpv library and owned PCM fixture"]
    fn constrained_local_source_plays_pcm_and_never_follows_network_playlist(){
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library path required");
        let fixture=std::env::var_os("PORTICO_TEST_PCM_FIXTURE").expect("owned fixture path required");
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        let inventory=engine.decoder_inventory().unwrap();assert!(inventory.iter().any(|codec|codec=="pcm_s32le"));assert!(inventory.iter().any(|codec|codec=="ffv1"));
        engine.load_local(Path::new(&fixture),1,0.0,true).unwrap();
        let until=std::time::Instant::now()+std::time::Duration::from_secs(5);
        let mut advanced=false;
        while std::time::Instant::now()<until {
            let observation=engine.observation();
            if observation.position_ms>10.0 {advanced=true;break}
            assert_ne!(observation.state,"error");
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        assert!(advanced,"native PCM did not advance");
        assert_eq!(engine.audio_facts().source_rate,Some(48000));assert_eq!(engine.audio_facts().output_rate,Some(48000));
        let ready=std::time::Instant::now()+std::time::Duration::from_secs(2);
        loop {
            let output=serde_json::to_value(engine.observation().observed_playback).unwrap();
            if output["audio"]["sampleRate"]==48000 {
                assert_eq!(output["audio"]["handling"],"pcm");assert_eq!(output["audio"]["channels"],2);
                assert!(output["audio"].get("bitDepth").is_none());assert!(output["audio"].get("bitPerfect").is_none());break
            }
            assert!(std::time::Instant::now()<ready,"current native output was not observed");
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        for gain in [f64::NAN,0.0,33.0]{assert_eq!(engine.execute(Command::Processing{generation:1,gain}),Err(Error::InvalidCommand));}
        engine.execute(Command::Processing{generation:1,gain:0.5}).unwrap();
        assert!(engine.property("af").unwrap().contains("@portico-leveling:"));
        assert!(engine.observation().leveling_applied,"actual PCM route did not confirm native leveling");
        engine.execute(Command::Processing{generation:1,gain:1.0}).unwrap();
        assert!(!engine.observation().leveling_applied);
        assert!(!engine.property("af").unwrap().contains("@portico-leveling:"));
        let first_entry=engine.expected_entry.load(Ordering::Acquire);assert!(first_entry>0);
        assert_eq!(engine.started_entry.load(Ordering::Acquire),first_entry);
        engine.frame_drawn(1);
        engine.load_local(Path::new(&fixture),2,0.0,true).unwrap();
        let loading=engine.audio_facts();assert_eq!(loading.generation,2);assert_eq!(loading.source_rate,None);assert_eq!(loading.output_rate,None);assert_eq!(loading.output_channels,None);assert_eq!(loading.output_format,None);
        assert_eq!(engine.execute(Command::Processing{generation:1,gain:0.25}),Err(Error::StaleGeneration));
        let second_entry=engine.expected_entry.load(Ordering::Acquire);assert!(second_entry>first_entry);
        assert_eq!(engine.frame_drawn.load(Ordering::Acquire),0);
        let until=std::time::Instant::now()+std::time::Duration::from_secs(5);
        let mut reloaded=false;
        while std::time::Instant::now()<until{
            let observation=engine.observation();
            if observation.state=="loading"{assert_eq!(observation.position_ms,0.0);assert_eq!(serde_json::to_value(observation.observed_playback).unwrap(),serde_json::json!({}));}
            if observation.state=="playing"&&observation.position_ms>10.0{reloaded=true;break}
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        assert!(reloaded,"second same-file native entry never advanced");
        assert_eq!(engine.started_entry.load(Ordering::Acquire),second_entry);
        assert_eq!(engine.observation().generation,2);
        let listener=std::net::TcpListener::bind("127.0.0.1:0").unwrap();listener.set_nonblocking(true).unwrap();
        let playlist=std::env::temp_dir().join(format!("desktop-denied-playlist-{}.media",std::process::id()));
        std::fs::write(&playlist,format!("#EXTM3U\n#EXTINF:1,fixture\nhttp://127.0.0.1:{}/forbidden.wav\n",listener.local_addr().unwrap().port())).unwrap();
        engine.load_local(&playlist,3,0.0,true).unwrap();
        let until=std::time::Instant::now()+std::time::Duration::from_secs(2);
        while std::time::Instant::now()<until {
            assert!(matches!(listener.accept(),Err(e) if e.kind()==std::io::ErrorKind::WouldBlock),"native local input opened a network reference");
            let _=engine.observation();
            std::thread::sleep(std::time::Duration::from_millis(10));
        }
        assert!(!matches!(engine.observation().state.as_str(),"playing"|"buffering"));
        std::fs::remove_file(playlist).unwrap();
        engine.retire().unwrap();
        assert!(!engine.matches_generation(3));
        assert_eq!(engine.load_local(Path::new(&fixture),4,0.0,true),Err(Error::EngineUnavailable));
        assert_eq!(engine.execute(Command::Play{generation:3}),Err(Error::EngineUnavailable));
        assert_eq!(engine.apply_equalizer(&EqualizerSettings::default()),Err(Error::EngineUnavailable));
        let observation=engine.observation();assert_eq!(observation.generation,0);assert!(!observation.native_surface_active);

    }
    #[test]
    #[ignore="requires an explicit native libmpv library and owned variable-frame-rate video"]
    fn paused_frame_steps_follow_native_vfr_frames_and_refuse_other_states(){
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library required");
        let fixture=std::env::var_os("PORTICO_TEST_VIDEO_FIXTURE").expect("owned VFR fixture required");
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        engine.load_local(Path::new(&fixture),1,0.0,false).unwrap();
        let wait=|after:Option<f64>,back:bool|{
            let until=std::time::Instant::now()+std::time::Duration::from_secs(5);
            loop{
                let value=engine.observation();assert_ne!(value.state,"error");
                if value.state=="paused"&&engine.restarted_generation.load(Ordering::Acquire)==1&&after.is_none_or(|a|if back{value.position_ms<a-1.0}else{value.position_ms>a+1.0}){return value.position_ms}
                assert!(std::time::Instant::now()<until,"native frame observation never completed: {value:?}");
                std::thread::sleep(std::time::Duration::from_millis(10));
            }
        };
        let start=wait(None,false);
        assert_eq!(engine.execute(Command::FrameStep{generation:2,direction:1}),Err(Error::StaleGeneration));
        assert_eq!(engine.execute(Command::FrameStep{generation:1,direction:0}),Err(Error::InvalidCommand));
        engine.execute(Command::FrameStep{generation:1,direction:1}).unwrap();let one=wait(Some(start),false);
        engine.execute(Command::FrameStep{generation:1,direction:1}).unwrap();let two=wait(Some(one),false);
        // This fixture's actual PTS are 0,100,400,500,1200,1700ms. A constant fps guess
        // cannot satisfy both observed intervals. The engine commands name no position.
        assert!((one-100.0).abs()<2.0,"first observed frame {one}");
        assert!((two-400.0).abs()<2.0,"second observed VFR frame {two}");
        engine.execute(Command::FrameStep{generation:1,direction:-1}).unwrap();let back=wait(Some(two),true);
        assert!((back-one).abs()<2.0,"back step returned {back}, expected actual prior frame {one}");
        engine.execute(Command::Play{generation:1}).unwrap();
        assert_eq!(engine.execute(Command::FrameStep{generation:1,direction:1}),Err(Error::InvalidCommand));
        engine.retire().unwrap();
    }

    #[test]
    #[ignore="requires explicit native libmpv and owned PCM; opens only ephemeral loopback HTTP"]
    fn native_http_progressive_plays_but_manifest_contents_never_open_another_resource(){
        use std::io::{Read,Write};
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native library required");
        let fixture=std::env::var_os("PORTICO_TEST_PCM_FIXTURE").expect("owned fixture required");
        let pcm=std::fs::read(fixture).unwrap();
        let listener=std::net::TcpListener::bind("127.0.0.1:0").unwrap();listener.set_nonblocking(true).unwrap();
        let port=listener.local_addr().unwrap().port();
        let done=Arc::new(AtomicBool::new(false));let stopping=done.clone();
        let escaped=Arc::new(AtomicBool::new(false));let escape=escaped.clone();
        let server=std::thread::spawn(move||{
            while !stopping.load(Ordering::Acquire){
                let (mut stream,_)=match listener.accept(){Ok(s)=>s,Err(e) if e.kind()==std::io::ErrorKind::WouldBlock=>{std::thread::sleep(std::time::Duration::from_millis(5));continue},Err(_)=>break};
                stream.set_read_timeout(Some(std::time::Duration::from_secs(1))).unwrap();stream.set_write_timeout(Some(std::time::Duration::from_secs(1))).unwrap();
                let mut request=[0u8;8192];let n=stream.read(&mut request).unwrap_or(0);let request=String::from_utf8_lossy(&request[..n]);
                let manifest=format!("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nhttp://127.0.0.1:{port}/escape\n#EXT-X-ENDLIST\n");
                let (mime,body)=if request.starts_with("GET /v1/media "){("audio/wav",pcm.as_slice())}else if request.starts_with("GET /v1/manifest "){("application/vnd.apple.mpegurl",manifest.as_bytes())}else{escape.store(true,Ordering::Release);("text/plain",b"denied".as_slice())};
                let header=format!("HTTP/1.1 200 OK\r\nContent-Type: {mime}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",body.len());
                let _=stream.write_all(header.as_bytes());let _=stream.write_all(body);
            }
        });
        let engine=Mpv::new(Api::open(Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        let run=||{
            let source=|path:&str,generation|Source{url:format!("http://127.0.0.1:{port}/v1/{path}"),generation,mode:"stream".into(),start_position_ms:0.0,autoplay:true};
            engine.execute(Command::Load{source:source("media",1)}).unwrap();
            let until=std::time::Instant::now()+std::time::Duration::from_secs(5);let mut advanced=false;
            while std::time::Instant::now()<until{let value=engine.observation();if value.position_ms>10.0{advanced=true;break}assert_ne!(value.state,"error");std::thread::sleep(std::time::Duration::from_millis(10));}
            assert!(advanced,"native progressive HTTP did not play");
            engine.execute(Command::Load{source:source("manifest",2)}).unwrap();
            let until=std::time::Instant::now()+std::time::Duration::from_secs(2);
            while std::time::Instant::now()<until{let _=engine.observation();assert!(!escaped.load(Ordering::Acquire),"native manifest escaped to a second resource");std::thread::sleep(std::time::Duration::from_millis(10));}
            assert!(!matches!(engine.observation().state.as_str(),"playing"|"buffering"));
            engine.retire().unwrap();
        };
        let result=std::panic::catch_unwind(std::panic::AssertUnwindSafe(run));
        done.store(true,Ordering::Release);server.join().unwrap();if let Err(error)=result{std::panic::resume_unwind(error)}
    }

}
