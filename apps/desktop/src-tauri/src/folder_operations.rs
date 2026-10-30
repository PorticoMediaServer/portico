//! Separate short-lived cancellation authority: a long copy never holds the
//! Downloads mutex against a viewer retirement or application close signal.
use crate::{boundary::{Error,Result},download_receipt::digest};
use ring::rand::{SecureRandom,SystemRandom};
use std::sync::{Arc,Mutex,atomic::{AtomicBool,Ordering}};

#[derive(Clone)]
pub struct FolderOperation{pub lease:String,token:String,current:Arc<AtomicBool>}
impl FolderOperation{pub fn current(&self)->bool{self.current.load(Ordering::Acquire)}}
#[derive(Default)]
pub struct FolderOperations{active:Mutex<Option<FolderOperation>>}
impl FolderOperations{
    pub fn begin(&self,lease:&str)->Result<String>{
        if !digest(lease){return Err(Error::InvalidCommand)}
        let mut active=self.active.lock().map_err(|_|Error::DownloadsUnavailable)?;
        if active.is_some(){return Err(Error::DownloadsConflict)}
        let mut bytes=[0u8;32];SystemRandom::new().fill(&mut bytes).map_err(|_|Error::DownloadsUnavailable)?;
        let token=bytes.iter().map(|byte|format!("{byte:02x}")).collect::<String>();
        *active=Some(FolderOperation{lease:lease.into(),token:token.clone(),current:Arc::new(AtomicBool::new(true))});Ok(token)
    }
    pub fn get(&self,token:&str)->Result<FolderOperation>{
        let active=self.active.lock().map_err(|_|Error::DownloadsUnavailable)?;
        active.as_ref().filter(|operation|operation.token==token&&operation.current()).cloned().ok_or(Error::DownloadsConflict)
    }
    pub fn cancel(&self,token:&str){
        if let Ok(active)=self.active.lock(){if let Some(operation)=active.as_ref().filter(|operation|operation.token==token){operation.current.store(false,Ordering::Release);}}
    }
    pub fn finish(&self,token:&str){
        if let Ok(mut active)=self.active.lock(){if active.as_ref().is_some_and(|operation|operation.token==token){if let Some(operation)=active.take(){operation.current.store(false,Ordering::Release);}}}
    }
    pub fn cancel_all(&self){if let Ok(active)=self.active.lock(){if let Some(operation)=active.as_ref(){operation.current.store(false,Ordering::Release);}}}
}
#[cfg(test)]
mod tests{
    use super::*;
    #[test]
    fn late_cancel_or_finish_cannot_widen_to_a_new_operation_or_another_lease(){
        let operations=FolderOperations::default();let first=operations.begin(&"a".repeat(64)).unwrap();let observed=operations.get(&first).unwrap();
        assert_eq!(observed.lease,"a".repeat(64));assert!(operations.begin(&"b".repeat(64)).is_err());
        operations.cancel(&first);assert!(!observed.current());assert!(operations.get(&first).is_err());operations.finish(&first);
        let second=operations.begin(&"b".repeat(64)).unwrap();let current=operations.get(&second).unwrap();
        operations.cancel(&first);operations.finish(&first);assert!(current.current());assert_eq!(current.lease,"b".repeat(64));
        operations.cancel_all();assert!(!current.current());operations.finish(&second);
    }
    #[test]
    fn completed_and_unknown_operations_never_grant_a_copy_lifetime(){
        let operations=FolderOperations::default();assert!(operations.begin("arbitrary/path").is_err());assert!(operations.get("unknown").is_err());
        let token=operations.begin(&"c".repeat(64)).unwrap();let observed=operations.get(&token).unwrap();operations.finish(&token);
        assert!(!observed.current());assert!(operations.get(&token).is_err());
    }
}
