use portico_desktop::{audio::compare_pcm,mpv::{Api,Mpv}};
use std::{path::Path,process::ExitCode};
fn run()->Result<(),String>{
    let args=std::env::args().skip(1).collect::<Vec<_>>();
    if args.len()!=7{return Err("usage: portico-engine-proof LIBMPV INPUT CAPTURE REFERENCE RATE CHANNELS BYTES_PER_SAMPLE".into());}
    let api=Api::open(Path::new(&args[0])).map_err(|e|format!("{e:?}"))?;
    let rate=args[4].parse().map_err(|_|"invalid rate")?;let channels=args[5].parse().map_err(|_|"invalid channels")?;
    let inputs=args[1].split('|').map(Path::new).collect::<Vec<_>>();
    Mpv::capture_playlist(api,&inputs,Path::new(&args[2]),rate,channels).map_err(|e|format!("capture: {e:?}"))?;
    let reference=std::fs::read(&args[3]).map_err(|_|"reference unavailable")?;
    let capture=std::fs::read(&args[2]).map_err(|_|"capture unavailable")?;
    let frames=compare_pcm(&reference,&capture,args[6].parse().map_err(|_|"invalid depth")?,channels as usize).map_err(|_|"PCM mismatch")?;
    println!("{{\"engineOutputPcmMatches\":true,\"frames\":{frames},\"sampleRate\":{rate},\"channels\":{channels},\"physicalExclusiveOutputVerified\":false}}");Ok(())
}
fn main()->ExitCode{match run(){Ok(())=>ExitCode::SUCCESS,Err(e)=>{eprintln!("{e}");ExitCode::FAILURE}}}
