//! One bounded DNS-SD browse, not a route admission or a source of credentials.
//! The existing client-core verifies the paired identity and a fresh signed route
//! proof before it uses a discovered origin. TXT fp identifies the identity key;
//! it is never a TLS certificate fingerprint.
use std::{collections::{BTreeMap,BTreeSet}, net::Ipv4Addr, sync::{Arc, Mutex, atomic::{AtomicBool, Ordering}}, time::{Duration, Instant, SystemTime, UNIX_EPOCH}};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use mdns_sd::{ServiceDaemon, ServiceEvent, ResolvedService};
use serde::Serialize;

const SERVICE:&str="_portico._tcp.local.";
const MAX_REQUEST:u64=9_007_199_254_740_991;
const WINDOW:Duration=Duration::from_millis(1800);
const OBSERVATION_MS:u64=1000;

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all="camelCase")]
pub struct DiscoveryRoute {
    pub base_url:String, pub server_id:String, pub fingerprint:String,
    pub port:u16, pub path:String, pub expires_at:u64,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum BrowseError {Unavailable, Cancelled}
type Result<T> = std::result::Result<T, BrowseError>;

/// A process-owned cancellation table, not an authority or persisted identity.
#[derive(Default)]
pub struct Browser {active:Mutex<BTreeMap<u64,Arc<AtomicBool>>>,cancelled:Mutex<BTreeSet<u64>>,closed:AtomicBool}
impl Browser {
    pub fn cancel(&self,id:u64){
        if id==0||id>MAX_REQUEST{return}
        if let Ok(mut cancelled)=self.cancelled.lock(){cancelled.insert(id);while cancelled.len()>128{cancelled.pop_first();}}
        if let Ok(active)=self.active.lock(){if let Some(cancel)=active.get(&id){cancel.store(true,Ordering::Release);}}
    }
    pub fn cancel_all(&self){
        self.closed.store(true,Ordering::Release);
        if let Ok(active)=self.active.lock(){for cancel in active.values(){cancel.store(true,Ordering::Release);}}
    }
    pub fn browse(&self,id:u64)->Result<Vec<DiscoveryRoute>>{
        if id==0||id>MAX_REQUEST{return Err(BrowseError::Unavailable)}
        let cancelled=Arc::new(AtomicBool::new(false));
        {
            let mut active=self.active.lock().map_err(|_|BrowseError::Unavailable)?;
            if active.len()>=2||active.contains_key(&id){return Err(BrowseError::Unavailable)}
            active.insert(id,cancelled.clone());
        }
        // Covers cancellation dispatched before the blocking worker starts.
        if self.closed.load(Ordering::Acquire)||self.cancelled.lock().map_err(|_|BrowseError::Unavailable)?.contains(&id){cancelled.store(true,Ordering::Release);}
        let result=browse(&cancelled,5353);
        if let Ok(mut active)=self.active.lock(){active.remove(&id);}
        result
    }
}
impl Drop for Browser {fn drop(&mut self){self.cancel_all();}}

struct Daemon(ServiceDaemon);
impl Drop for Daemon {
    fn drop(&mut self){
        let _=self.0.stop_browse(SERVICE);
        if let Ok(done)=self.0.shutdown(){let _=done.recv_timeout(Duration::from_secs(1));}
    }
}
fn now()->Result<u64>{SystemTime::now().duration_since(UNIX_EPOCH).map(|v|v.as_millis() as u64).map_err(|_|BrowseError::Unavailable)}
fn browse(cancelled:&AtomicBool,port:u16)->Result<Vec<DiscoveryRoute>>{
    if cancelled.load(Ordering::Acquire){return Err(BrowseError::Cancelled)}
    let daemon=Daemon(ServiceDaemon::new_with_port(port).map_err(|_|BrowseError::Unavailable)?);
    let monitor=daemon.0.monitor().map_err(|_|BrowseError::Unavailable)?;
    let events=daemon.0.browse(SERVICE).map_err(|_|BrowseError::Unavailable)?;
    let mut deadline=Instant::now()+WINDOW;
    let mut found=BTreeMap::<String,Vec<DiscoveryRoute>>::new();
    while Instant::now()<deadline {
        if cancelled.load(Ordering::Acquire){return Err(BrowseError::Cancelled)}
        while let Ok(event)=monitor.try_recv(){
            if matches!(event,mdns_sd::DaemonEvent::Error(_)){return Err(BrowseError::Unavailable)}
        }
        match events.recv_timeout(Duration::from_millis(40)){
            Ok(ServiceEvent::ServiceResolved(service))=>{
                deadline=deadline.min(Instant::now()+Duration::from_millis(200));
                if !found.contains_key(&service.fullname)&&found.len()>=64{return Err(BrowseError::Unavailable)}
                // This is a short observation window, not a claim about DNS TTL.
                // Repeated cache notifications never extend the first observation.
                let at=now()?;
                let previous=found.get(&service.fullname).and_then(|v|v.first()).map(|v|v.expires_at);
                let mut rows=project(&service,at);
                if let Some(expires)=previous{for row in &mut rows{row.expires_at=expires;}}
                found.insert(service.fullname.clone(),rows);
            }
            Ok(ServiceEvent::ServiceRemoved(_,name))=>{found.remove(&name);}
            _=>{}
        }
    }
    if cancelled.load(Ordering::Acquire){return Err(BrowseError::Cancelled)}
    let at=now()?;
    let mut rows:Vec<_>=found.into_values().flatten().filter(|r|r.expires_at>at).collect();
    rows.sort_by(|a,b|(&a.fingerprint,&a.server_id,&a.base_url).cmp(&(&b.fingerprint,&b.server_id,&b.base_url)));
    rows.dedup();
    if rows.len()>64{return Err(BrowseError::Unavailable)}
    Ok(rows)
}
fn private_v4(ip:&Ipv4Addr)->bool {ip.is_private()||ip.is_link_local()}
fn encoded_key(value:&str)->bool {
    URL_SAFE_NO_PAD.decode(value).is_ok_and(|bytes|bytes.len()==32&&URL_SAFE_NO_PAD.encode(bytes)==value)
}
fn project(service:&ResolvedService,at:u64)->Vec<DiscoveryRoute>{
    let value=|key|service.get_property_val_str(key);
    let Some(id)=value("id").filter(|id|id.strip_prefix("srv_").is_some_and(encoded_key))else{return vec![]};
    let Some(fp)=value("fp").filter(|fp|encoded_key(fp))else{return vec![]};
    if service.ty_domain!=SERVICE||!service.fullname.ends_with(SERVICE)||value("v")!=Some("1")
        ||value("path")!=Some("/")||service.port==0||value("port")!=Some(service.port.to_string().as_str()) {
        return vec![]
    }
    // IPv6 link-local addresses require a native interface scope that the core
    // URL admission does not accept. Do not strip it or guess a routable address.
    let mut addresses:Vec<_>=service.get_addresses_v4().into_iter().filter(private_v4).collect();
    addresses.sort();addresses.truncate(8);
    addresses.into_iter().map(|ip|DiscoveryRoute{
        base_url:format!("https://{ip}:{}",service.port),server_id:id.into(),fingerprint:fp.into(),
        port:service.port,path:"/".into(),expires_at:at.saturating_add(OBSERVATION_MS),
    }).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use mdns_sd::{ServiceInfo,IntoTxtProperties};
    fn service(port:u16)->ServiceInfo {
        ServiceInfo::new(SERVICE,"portico-test","portico-test.local.","192.168.77.8",port,
            [("v","1"),("id",&format!("srv_{}",URL_SAFE_NO_PAD.encode([4;32]))),
             ("fp",&URL_SAFE_NO_PAD.encode([7;32])),("port",&port.to_string()),("path","/")].as_slice()).unwrap()
    }
    #[test]
    fn exact_server_txt_projects_only_private_ipv4_and_no_trust_or_display_fields(){
        let value=service(10443).as_resolved_service();let rows=project(&value,5000);
        assert_eq!(rows.len(),1);assert_eq!(rows[0].base_url,"https://192.168.77.8:10443");
        assert_eq!(rows[0].expires_at,6000);
        let json=serde_json::to_value(&rows[0]).unwrap();
        assert_eq!(json.as_object().unwrap().len(),6);assert!(json.get("trusted").is_none());assert!(json.get("name").is_none());
        for key in ["v","path","id","fp","port"]{
            let mut value=service(10443).as_resolved_service();
            let changed:Vec<(String,String)>=value.txt_properties.iter().map(|p|(p.key().into(),if p.key()==key{"hostile".into()}else{p.val_str().into()})).collect();
            value.txt_properties=changed.as_slice().into_txt_properties();assert!(project(&value,5000).is_empty());
        }
        let mut value=service(10443).as_resolved_service();value.port=10444;assert!(project(&value,5000).is_empty());
        for ip in [Ipv4Addr::new(8,8,8,8),Ipv4Addr::LOCALHOST,Ipv4Addr::new(100,110,122,17),Ipv4Addr::UNSPECIFIED]{assert!(!private_v4(&ip));}
    }
    #[test]
    fn cancellation_and_request_bounds_do_not_open_a_daemon(){
        assert_eq!(browse(&AtomicBool::new(true),5353),Err(BrowseError::Cancelled));
        let browser=Browser::default();assert_eq!(browser.browse(0),Err(BrowseError::Unavailable));
        assert_eq!(browser.browse(MAX_REQUEST+1),Err(BrowseError::Unavailable));
        let cancelled=Arc::new(AtomicBool::new(false));browser.active.lock().unwrap().insert(4,cancelled.clone());
        browser.cancel(5);assert!(!cancelled.load(Ordering::Acquire));browser.cancel(4);assert!(cancelled.load(Ordering::Acquire));
        assert_eq!(browser.browse(5),Err(BrowseError::Cancelled));
        browser.cancel_all();assert_eq!(browser.browse(6),Err(BrowseError::Cancelled));
    }
    #[test]
    #[ignore="explicit private runner multicast smoke; no endpoint playback or real LAN claim"]
    fn actual_bounded_browse_retires_its_daemon_after_cancellation(){
        let cancelled=Arc::new(AtomicBool::new(false));let flag=cancelled.clone();
        let started=Instant::now();let worker=std::thread::spawn(move||browse(&flag,15353));
        std::thread::sleep(Duration::from_millis(100));cancelled.store(true,Ordering::Release);
        assert_eq!(worker.join().unwrap(),Err(BrowseError::Cancelled));assert!(started.elapsed()<Duration::from_secs(3));
    }
    #[test]
    #[ignore="explicit private runner DNS-SD fixture; candidate only, no server endpoint or trust claim"]
    fn actual_dns_sd_publisher_is_observed_as_an_untrusted_candidate(){
        let publisher=Daemon(ServiceDaemon::new_with_port(15354).unwrap());
        let properties:Vec<_>=service(10443).get_properties().iter().map(|p|(p.key().to_owned(),p.val_str().to_owned())).collect();
        let fixture=ServiceInfo::new(SERVICE,"portico-test","portico-test.local.","",10443,properties.as_slice()).unwrap().enable_addr_auto();
        publisher.0.register(fixture).unwrap();
        let rows=browse(&AtomicBool::new(false),15354).unwrap();
        assert!(!rows.is_empty(),"isolated smoke requires an actual private IPv4 interface");
        for row in rows{assert_eq!(row.server_id,format!("srv_{}",URL_SAFE_NO_PAD.encode([4;32])));assert_eq!(row.port,10443);assert!(row.expires_at>now().unwrap());}
    }
}
