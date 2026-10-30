//! The native file/receipt boundary can be checked without loading a window,
//! WebView, audio route or libmpv. These are the same modules used by the app.
#[path = "../../src/boundary.rs"]
pub mod boundary;
#[path = "../../src/download_receipt.rs"]
pub mod download_receipt;
#[path = "../../src/download_store.rs"]
pub mod download_store;
#[path = "../../src/download_folder.rs"]
pub mod download_folder;
#[path = "../../src/folder_operations.rs"]
pub mod folder_operations;
#[path = "../../src/download_commands.rs"]
pub mod download_commands;
#[path = "../../src/equalizer.rs"]
pub mod equalizer;
#[path = "../../src/equalizer_store.rs"]
pub mod equalizer_store;
#[path = "../../src/network_cost.rs"]
pub mod network_cost;
#[path = "../../src/device_facts.rs"]
pub mod device_facts;
#[path = "../../src/installation_store.rs"]
pub mod installation_store;
#[path = "../../src/measurement.rs"]
pub mod measurement;
#[path = "../../src/shutdown.rs"]
pub mod shutdown;
