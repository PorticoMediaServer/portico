//! Debug-only owned-spike stages. No command, URL, media path or native exception is logged.
pub(crate) fn record(stage:&str,code:i32){
    #[cfg(debug_assertions)]
    if let Some(path)=std::env::var_os("PORTICO_SPIKE_REPORT"){
        use std::io::Write;
        let path=std::path::PathBuf::from(path).with_extension("native.log");
        if let Ok(mut file)=std::fs::OpenOptions::new().create(true).append(true).open(path){let _=writeln!(file,"{stage} {code}");}
    }
    #[cfg(not(debug_assertions))]
    let _=(stage,code);
}
