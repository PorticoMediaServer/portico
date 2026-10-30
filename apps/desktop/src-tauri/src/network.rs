//! Read-only OS network facts. Unknown never grants automatic unmetered transfer.
pub use crate::network_cost::NetworkCost;
#[cfg(target_os="linux")]
use crate::network_cost::network_manager_cost;
#[cfg(target_os="windows")]
#[path="network_windows.rs"]mod windows;

#[cfg(target_os="linux")]
pub fn current()->NetworkCost {
    use gtk::{gio,glib::{self,prelude::*}};
    let read=(||{
        let bus=gio::bus_get_sync(gio::BusType::System,gio::Cancellable::NONE).ok()?;
        let reply=bus.call_sync(Some("org.freedesktop.NetworkManager"),"/org/freedesktop/NetworkManager",
            "org.freedesktop.DBus.Properties","GetAll",Some(&("org.freedesktop.NetworkManager",).to_variant()),
            None,gio::DBusCallFlags::NO_AUTO_START,1000,gio::Cancellable::NONE).ok()?;
        let (values,)=reply.get::<(std::collections::HashMap<String,glib::Variant>,)>()?;
        Some(network_manager_cost(values.get("State").and_then(|v|v.get()),values.get("Metered").and_then(|v|v.get())))
    })();
    read.unwrap_or_default()
}

#[cfg(target_os="macos")]
unsafe extern "C" {fn portico_network_start();fn portico_network_stop();fn portico_network_cost()->u32;}
/// Called only on the platform main thread during owned application setup/teardown.
pub fn start(){#[cfg(target_os="macos")]unsafe{portico_network_start();}}
pub fn stop(){#[cfg(target_os="macos")]unsafe{portico_network_stop();}}
#[cfg(target_os="macos")]
pub fn current()->NetworkCost{match unsafe{portico_network_cost()}{1=>NetworkCost::Metered,2=>NetworkCost::Unmetered,_=>NetworkCost::Unknown}}
#[cfg(target_os="windows")]
pub fn current()->NetworkCost{windows::current()}
#[cfg(not(any(target_os="linux",target_os="macos",target_os="windows")))]
pub fn current()->NetworkCost{NetworkCost::Unknown}
