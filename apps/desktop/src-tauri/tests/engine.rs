use portico_desktop::{audio::AudioOutput,boundary::{Command,Error,Source},mpv::{Api,Mpv,VideoTarget}};
use std::{path::Path,time::Duration};

#[test]
#[ignore = "requires the explicitly pinned native libmpv artifact"]
fn actual_engine_initializes_with_hardened_options_and_rejects_stale_commands() {
    let library=std::env::var("PORTICO_MPV_LIBRARY").expect("set pinned libmpv path");
    let api=Api::open(Path::new(&library)).unwrap();
    let engine=Mpv::new(api,&AudioOutput::default(),VideoTarget::Headless).unwrap();
    assert_eq!(engine.observation().state,"idle");
    assert_eq!(engine.execute(Command::Play{generation:1}),Err(Error::StaleGeneration));
    assert_eq!(engine.execute(Command::Load{source:Source{url:"file:///etc/passwd".into(),generation:1,mode:"direct".into(),start_position_ms:0.0,autoplay:true}}),Err(Error::InvalidSource));
    std::thread::sleep(Duration::from_millis(10));
    let json=serde_json::to_string(&engine.observation()).unwrap();
    assert!(!json.contains("url"));assert!(!json.contains("path"));
}
