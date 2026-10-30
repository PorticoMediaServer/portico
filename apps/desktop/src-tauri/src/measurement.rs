//! Private DEV transport, not a measurement/comparison implementation. The
//! original public gallery is mounted by the real app and measured unchanged.
use crate::boundary::{Error,Result};
use serde_json::Value;
use std::{path::{Path,PathBuf},sync::Mutex,io::Write};

#[derive(Default)]
pub struct MeasurementState(Mutex<Option<Job>>);
struct Job{route:String,options:Value,output:PathBuf,width:f64,height:f64,written:bool}
impl MeasurementState{
    pub fn from_env()->Result<Self>{
        if !cfg!(debug_assertions){return Ok(Self::default());}
        let Some(input)=std::env::var_os("PORTICO_DESKTOP_MEASURE_REQUEST") else{return Ok(Self::default())};
        let output=std::env::var_os("PORTICO_DESKTOP_MEASURE_OUTPUT").ok_or(Error::InvalidCommand)?;
        let data=std::env::var_os("PORTICO_SPIKE_DATA").ok_or(Error::InvalidCommand)?;
        let route=std::env::var("PORTICO_DESKTOP_MEASURE_ROUTE").map_err(|_|Error::InvalidCommand)?;
        Self::open(Path::new(&input),Path::new(&output),Path::new(&data),&route)
    }
    fn open(input:&Path,output:&Path,data:&Path,route:&str)->Result<Self>{
        let invalid=||Error::InvalidCommand;
        if !input.is_absolute()||!output.is_absolute()||!data.is_absolute()||output.exists()||output.file_name().is_none(){return Err(invalid())}
        let parent=input.parent().ok_or_else(invalid)?.canonicalize().map_err(|_|invalid())?;
        if output.parent().ok_or_else(invalid)?.canonicalize().map_err(|_|invalid())?!=parent||!data.canonicalize().map_err(|_|invalid())?.starts_with(&parent){return Err(invalid())}
        let meta=std::fs::symlink_metadata(input).map_err(|_|invalid())?;
        if !meta.is_file()||meta.len()>65536{return Err(invalid())}
        let options:Value=serde_json::from_slice(&std::fs::read(input).map_err(|_|invalid())?).map_err(|_|invalid())?;
        // Only canonical collector arguments cross the transport. No reference,
        // file path, script, style or authentication input is sent to WebView.
        let fields=options.as_object().ok_or_else(invalid)?;
        if fields.keys().any(|key|!matches!(key.as_str(),"selector"|"expectedViewport"|"anchors"|"repeats"))||options["selector"].as_str().is_none(){return Err(invalid())}
        let size=|name:&str|options["expectedViewport"][name].as_u64().filter(|n|(600..=7680).contains(n)).map(|n|n as f64).ok_or_else(invalid);
        let width=size("width")?;let height=size("height")?;
        let url=url::Url::parse(&format!("http://127.0.0.1:14229{route}")).map_err(|_|invalid())?;
        let query:Vec<_>=url.query_pairs().collect();
        if !route.starts_with("/dev/kit?")||url.path()!="/dev/kit"||url.fragment().is_some()||
            query.iter().any(|(key,_)|!matches!(key.as_ref(),"capture"|"family"|"case"|"width"|"height"))||
            !query.iter().any(|(key,value)|key=="capture"&&value=="true")||
            ["family","case","width","height","capture"].iter().any(|key|query.iter().filter(|(name,_)|name.as_ref()==*key).count()!=1)||
            !query.iter().any(|(key,value)|key=="width"&&value.parse::<f64>().ok()==Some(width))||
            !query.iter().any(|(key,value)|key=="height"&&value.parse::<f64>().ok()==Some(height)){return Err(invalid())}
        Ok(Self(Mutex::new(Some(Job{route:route.into(),options,output:parent.join(output.file_name().unwrap()),width,height,written:false}))))
    }
    pub fn window(&self)->Result<(String,f64,f64)>{
        let guard=self.0.lock().map_err(|_|Error::InvalidCommand)?;
        Ok(guard.as_ref().map(|job|(job.route.clone(),job.width,job.height)).unwrap_or(("index.html".into(),1440.,900.)))
    }
    fn caller(job:&Job,url:&str)->Result<()>{
        if !cfg!(debug_assertions){return Err(Error::ForbiddenOrigin)}
        crate::boundary::local_origin("main",url,true)?;
        let url=url::Url::parse(url).map_err(|_|Error::ForbiddenOrigin)?;
        if url.scheme()!="http"||url.host_str()!=Some("127.0.0.1")||url.port()!=Some(14229)||format!("{}?{}",url.path(),url.query().unwrap_or(""))!=job.route{return Err(Error::ForbiddenOrigin)}
        Ok(())
    }
    pub fn options(&self,url:&str)->Result<Option<Value>>{
        let guard=self.0.lock().map_err(|_|Error::InvalidCommand)?;
        let Some(job)=guard.as_ref()else{return Ok(None)};
        Self::caller(job,url)?;Ok(Some(job.options.clone()))
    }
    pub fn write(&self,url:&str,report:Value)->Result<()>{
        let mut guard=self.0.lock().map_err(|_|Error::InvalidCommand)?;
        let job=guard.as_mut().ok_or(Error::InvalidCommand)?;Self::caller(job,url)?;
        // Bound opaque transport only; shared contract.mjs validates the actual
        // facts in WebView and compare-files.mjs validates them offline again.
        let bytes=serde_json::to_vec(&report).map_err(|_|Error::InvalidCommand)?;
        if job.written||bytes.len()>16*1024*1024{return Err(Error::InvalidCommand)}
        let mut opts=std::fs::OpenOptions::new();opts.write(true).create_new(true);
        #[cfg(unix)]{use std::os::unix::fs::OpenOptionsExt;opts.mode(0o600);}
        let mut file=opts.open(&job.output).map_err(|_|Error::InvalidCommand)?;
        if file.write_all(&bytes).and_then(|_|file.sync_all()).is_err(){drop(file);let _=std::fs::remove_file(&job.output);return Err(Error::InvalidCommand)}
        job.written=true;Ok(())
    }
}

#[cfg(test)]mod tests{
    use super::*;
    #[test]fn private_transport_never_overwrites_inputs_or_accepts_foreign_routes(){
        let root=std::env::temp_dir().join(format!("desktop-measure-{}",std::process::id()));std::fs::create_dir_all(root.join("data")).unwrap();
        let input=root.join("request.json");let output=root.join("actual.json");
        std::fs::write(&input,r##"{"selector":"#root","expectedViewport":{"width":1200,"height":800},"anchors":{},"repeats":[]}"##).unwrap();
        let route="/dev/kit?capture=true&family=accounts&case=sign-in&width=1200&height=800";
        let state=MeasurementState::open(&input,&output,&root.join("data"),route).unwrap();
        assert!(MeasurementState::open(&input,&input,&root.join("data"),route).is_err());
        assert!(MeasurementState::open(&input,&output,&root.join("data"),"/sign-in").is_err());
        assert!(MeasurementState::open(&input,&output,&root.join("data"),&format!("{route}&capture=true")).is_err());
        assert!(MeasurementState::open(&input,&output,&root.join("data"),&route.replace("width=1200","width=1300")).is_err());
        assert!(MeasurementState::open(&input,&output,&root,route).is_ok());
        std::fs::write(root.join("bad.json"),r##"{"selector":"#root","script":"no","expectedViewport":{"width":1200,"height":800}}"##).unwrap();
        assert!(MeasurementState::open(&root.join("bad.json"),&output,&root.join("data"),route).is_err());
        assert!(state.options("http://127.0.0.1:14229/sign-in").is_err());
        assert!(state.options("https://external.example/dev/kit").is_err());
        assert!(state.options("tauri://localhost/dev/kit").is_err());
        let url=format!("http://127.0.0.1:14229{route}");assert!(state.options(&url).unwrap().is_some());
        let value=serde_json::json!({"version":1,"scope":"screen"});state.write(&url,value.clone()).unwrap();assert!(state.write(&url,value).is_err());
        assert!(MeasurementState::open(&input,&output,&root.join("data"),route).is_err());
        std::fs::remove_dir_all(root).unwrap();
    }
}
