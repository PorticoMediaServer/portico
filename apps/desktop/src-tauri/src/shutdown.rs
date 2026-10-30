use std::sync::atomic::{AtomicU8,Ordering};

/// Renderer retirement is distinct from native teardown. Existing captured
/// owners may release their leases while the renderer handles an OS close.
#[derive(Default)]
pub struct ShutdownState(AtomicU8);
impl ShutdownState{
    pub fn running(&self)->bool{self.0.load(Ordering::Acquire)==0}
    pub fn accepts_commands(&self)->bool{self.0.load(Ordering::Acquire)<2}
    pub fn waiting_for_web(&self)->bool{self.0.load(Ordering::Acquire)==1}
    pub fn finished(&self)->bool{self.0.load(Ordering::Acquire)==3}
    pub fn request_web(&self)->bool{self.0.compare_exchange(0,1,Ordering::AcqRel,Ordering::Acquire).is_ok()}
    pub fn begin_native(&self)->bool{self.0.fetch_update(Ordering::AcqRel,Ordering::Acquire,|phase|if phase<2{Some(2)}else{None}).is_ok()}
    pub fn finish(&self){let _=self.0.compare_exchange(2,3,Ordering::AcqRel,Ordering::Acquire);}
}

#[cfg(test)]mod tests{
    use super::*;
    #[test]fn renderer_releases_remain_authorized_until_native_acknowledged_teardown(){
        let state=ShutdownState::default();assert!(state.running());assert!(state.request_web());assert!(!state.request_web());
        assert!(!state.running());assert!(state.waiting_for_web());assert!(state.accepts_commands());
        state.finish();assert!(!state.finished());
        assert!(state.begin_native());assert!(!state.accepts_commands());assert!(!state.waiting_for_web());assert!(!state.begin_native());
        state.finish();assert!(state.finished());assert!(!state.request_web());assert!(!state.begin_native());
    }
    #[test]fn prepared_close_can_begin_directly_and_ack_timeout_race_claims_one_teardown(){
        let direct=ShutdownState::default();assert!(direct.begin_native());direct.finish();assert!(direct.finished());
        let state=std::sync::Arc::new(ShutdownState::default());state.request_web();
        let gate=std::sync::Arc::new(std::sync::Barrier::new(3));
        let workers:Vec<_>=(0..2).map(|_|{let state=state.clone();let gate=gate.clone();std::thread::spawn(move||{gate.wait();state.begin_native()})}).collect();
        gate.wait();let count:usize=workers.into_iter().map(|worker|usize::from(worker.join().unwrap())).sum();assert_eq!(count,1);
        state.finish();assert!(state.finished());
    }
}
