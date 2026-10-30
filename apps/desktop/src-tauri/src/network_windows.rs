//! Read-only preferred Internet connection cost, not a claim about a specific DAC,
//! playback resource route, throughput or an unexecuted Windows qualification.
//! API: learn.microsoft.com/windows/win32/api/netlistmgr/nf-netlistmgr-inetworkcostmanager-getcost
use crate::network_cost::{NetworkCost,windows_cost};
use ::windows::Win32::{
    Foundation::RPC_E_CHANGED_MODE,
    Networking::NetworkListManager::{INetworkCostManager,NetworkListManager},
    System::Com::{CoCreateInstance,CoInitializeEx,CoUninitialize,CLSCTX_ALL,COINIT_MULTITHREADED},
};

pub fn current()->NetworkCost {
    // Objects never leave this worker/apartment. Balance only our successful init,
    // including S_FALSE; an existing STA belongs to its original caller.
    unsafe {
        let initialized=CoInitializeEx(None,COINIT_MULTITHREADED);
        if initialized.is_err()&&initialized!=RPC_E_CHANGED_MODE{return NetworkCost::Unknown}
        struct Apartment(bool);
        impl Drop for Apartment {fn drop(&mut self){if self.0{unsafe{CoUninitialize()}}}}
        let _apartment=Apartment(initialized.is_ok());
        let observed=(||->::windows::core::Result<u32>{
            let manager:INetworkCostManager=CoCreateInstance(&NetworkListManager,None,CLSCTX_ALL)?;
            let mut cost=0;
            manager.GetCost(&mut cost,std::ptr::null())?;
            Ok(cost)
        })();
        windows_cost(observed.ok())
    }
}
