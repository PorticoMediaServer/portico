//! Native-only directory relocation. No path is accepted from web IPC.
use crate::{boundary::{Error,Result},download_receipt::digest,download_store::open_file};
use ring::{rand::{SecureRandom,SystemRandom},digest::{Context,SHA256}};
use rusqlite::{Connection,OptionalExtension,Transaction,params};
use std::{fs,io::{Read,Write,Seek,SeekFrom},path::{Path,PathBuf}};

const SQL:&str="
CREATE TABLE IF NOT EXISTS download_folder(id INTEGER PRIMARY KEY CHECK(id=1),path TEXT NOT NULL) STRICT;
CREATE TABLE IF NOT EXISTS download_folder_cleanup(path TEXT PRIMARY KEY) STRICT;
CREATE TABLE IF NOT EXISTS download_folder_cleanup_files(path TEXT NOT NULL REFERENCES download_folder_cleanup(path) ON DELETE CASCADE,name TEXT NOT NULL,PRIMARY KEY(path,name)) STRICT;
";
fn unavailable<T>(_:T)->Error{Error::DownloadsUnavailable}
fn text(path:&Path)->Result<&str>{
    let value=path.to_str().ok_or(Error::DownloadsUnavailable)?;
    if !path.is_absolute()||value.len()>32768||value.chars().any(char::is_control){return Err(Error::DownloadsUnavailable)}
    Ok(value)
}
fn directory(path:&Path)->Result<()>{
    let meta=fs::symlink_metadata(path).map_err(unavailable)?;
    if !meta.is_dir()||meta.file_type().is_symlink(){return Err(Error::DownloadsUnavailable)}Ok(())
}
fn owned_name(value:&str)->bool{value.rsplit_once('.').is_some_and(|(id,ext)|digest(id)&&matches!(ext,"part"|"media"|"retry"))}
fn names(connection:&Connection,path:&Path)->Result<Vec<String>>{
    let mut query=connection.prepare("SELECT id FROM download_entries UNION SELECT id FROM download_garbage").map_err(unavailable)?;
    let ids=query.query_map([],|row|row.get::<_,String>(0)).map_err(unavailable)?.collect::<rusqlite::Result<Vec<_>>>().map_err(unavailable)?;
    let mut names=Vec::new();
    for id in ids{
        if !digest(&id){return Err(Error::DownloadsUnavailable)}
        for ext in ["part","media","retry"]{
            let name=format!("{id}.{ext}");
            match fs::symlink_metadata(path.join(&name)){
                Ok(meta) if meta.is_file()&&!meta.file_type().is_symlink()=>names.push(name),
                Err(error) if error.kind()==std::io::ErrorKind::NotFound=>{},
                _=>return Err(Error::DownloadsUnavailable),
            }
        }
    }
    Ok(names)
}
fn queue(tx:&Transaction,path:&Path,names:&[String])->Result<()>{
    tx.execute("INSERT INTO download_folder_cleanup VALUES(?1)",[text(path)?]).map_err(unavailable)?;
    for name in names{tx.execute("INSERT INTO download_folder_cleanup_files VALUES(?1,?2)",params![text(path)?,name]).map_err(unavailable)?;}
    Ok(())
}
fn sync_directory(path:&Path)->Result<()>{
    #[cfg(unix)]{fs::File::open(path).map_err(unavailable)?.sync_all().map_err(unavailable)}
    #[cfg(not(unix))]{let _=path;Err(Error::Unsupported)}
}
/// Retry only the exact files recorded before a copy or pointer commit. Unknown
/// files, links, absent volumes and the active directory are never cleaned.
pub fn cleanup(connection:&Connection,active:&Path)->Result<()>{
    let mut query=connection.prepare("SELECT path FROM download_folder_cleanup").map_err(unavailable)?;
    let paths=query.query_map([],|row|row.get::<_,String>(0)).map_err(unavailable)?.collect::<rusqlite::Result<Vec<_>>>().map_err(unavailable)?;
    drop(query);
    for raw in paths{
        let path=PathBuf::from(&raw);if path==active||text(&path).is_err()||directory(&path).is_err(){continue;}
        let mut query=connection.prepare("SELECT name FROM download_folder_cleanup_files WHERE path=?1").map_err(unavailable)?;
        let files=query.query_map([&raw],|row|row.get::<_,String>(0)).map_err(unavailable)?.collect::<rusqlite::Result<Vec<_>>>().map_err(unavailable)?;
        drop(query);let mut finished=true;
        for name in files{
            if !owned_name(&name){return Err(Error::DownloadsUnavailable)}
            let file=path.join(name);
            match fs::symlink_metadata(&file){
                Ok(meta) if meta.is_file()&&!meta.file_type().is_symlink()=>{finished&=fs::remove_file(file).is_ok();},
                Err(error) if error.kind()==std::io::ErrorKind::NotFound=>{},
                _=>finished=false,
            }
        }
        if finished{
            if sync_directory(&path).is_err(){continue}
            // A user may have placed unrelated files in the old directory. Keep them.
            let _=fs::remove_dir(&path);
            connection.execute("DELETE FROM download_folder_cleanup WHERE path=?1",[raw]).map_err(unavailable)?;
        }
    }
    Ok(())
}
pub fn open(connection:&Connection,default:&Path)->Result<PathBuf>{
    connection.execute_batch(SQL).map_err(unavailable)?;
    let saved:Option<String>=connection.query_row("SELECT path FROM download_folder WHERE id=1",[],|row|row.get(0)).optional().map_err(unavailable)?;
    let active=match saved{Some(path)=>PathBuf::from(path),None=>{
        connection.execute("INSERT INTO download_folder VALUES(1,?1)",[text(default)?]).map_err(unavailable)?;default.to_path_buf()
    }};
    text(&active)?;directory(&active)?;cleanup(connection,&active)?;Ok(active)
}
pub fn retained_bytes(connection:&Connection,active:&Path)->Result<u64>{
    let mut query=connection.prepare("SELECT path,name FROM download_folder_cleanup_files").map_err(unavailable)?;
    let files=query.query_map([],|row|Ok((row.get::<_,String>(0)?,row.get::<_,String>(1)?))).map_err(unavailable)?;
    let mut bytes=0u64;
    for file in files{
        let (path,name)=file.map_err(unavailable)?;let directory=PathBuf::from(path);
        if directory==active{continue}if !owned_name(&name){return Err(Error::DownloadsUnavailable)}
        match fs::symlink_metadata(directory.join(name)){
            Ok(meta) if meta.is_file()&&!meta.file_type().is_symlink()=>bytes=bytes.checked_add(meta.len()).ok_or(Error::DownloadsUnavailable)?,
            Err(error) if error.kind()==std::io::ErrorKind::NotFound=>{},
            _=>return Err(Error::DownloadsUnavailable),
        }
    }
    Ok(bytes)
}
/// Called only with the native chooser's selected directory. Copy/flush first,
/// then commit the active pointer; failed/cancelled copies retain every original.
pub fn relocate(connection:&mut Connection,source:&Path,selected:&Path,current:impl Fn()->bool)->Result<PathBuf>{
    #[cfg(not(unix))]{let _=(connection,source,selected,current);return Err(Error::Unsupported)}
    #[cfg(unix)]{
        directory(source)?;let parent=fs::canonicalize(selected).map_err(unavailable)?;directory(&parent)?;text(&parent)?;
        if !current(){return Err(Error::DownloadsConflict)}
        let files=names(connection,source)?;
        let required=files.iter().try_fold(0u64,|sum,name|sum.checked_add(fs::symlink_metadata(source.join(name)).map_err(unavailable)?.len()).ok_or(Error::DownloadsUnavailable))?;
        if required>crate::download_store::disk_space(&parent)?.1{return Err(Error::StorageFull)}
        let mut nonce=[0u8;16];SystemRandom::new().fill(&mut nonce).map_err(unavailable)?;
        let nonce=nonce.iter().map(|byte|format!("{byte:02x}")).collect::<String>();
        let destination=parent.join(format!("Portico-{nonce}"));
        use std::os::unix::fs::DirBuilderExt;
        fs::DirBuilder::new().mode(0o700).create(&destination).map_err(unavailable)?;
        let staged=(||{
            let tx=connection.transaction().map_err(unavailable)?;queue(&tx,&destination,&files)?;tx.commit().map_err(unavailable)
        })();
        if let Err(error)=staged{let _=fs::remove_dir(&destination);return Err(error)}
        let result=(||{
            let mut buffer=[0u8;262144];
            for name in &files{
                let mut input=open_file(&source.join(name),false,false)?;
                let expected=input.metadata().map_err(unavailable)?.len();
                let mut output=open_file(&destination.join(name),true,true)?;
                let mut bytes=0u64;let mut copied=Context::new(&SHA256);
                loop{
                    if !current(){return Err(Error::DownloadsConflict)}
                    let count=input.read(&mut buffer).map_err(unavailable)?;if count==0{break;}
                    output.write_all(&buffer[..count]).map_err(unavailable)?;copied.update(&buffer[..count]);bytes=bytes.checked_add(count as u64).ok_or(Error::DownloadsUnavailable)?;
                }
                if bytes!=expected||input.metadata().map_err(unavailable)?.len()!=expected{return Err(Error::DownloadsConflict)}
                output.sync_all().map_err(unavailable)?;
                output.seek(SeekFrom::Start(0)).map_err(unavailable)?;let mut verified=Context::new(&SHA256);
                loop{if !current(){return Err(Error::DownloadsConflict)}let count=output.read(&mut buffer).map_err(unavailable)?;if count==0{break}verified.update(&buffer[..count]);}
                if copied.finish().as_ref()!=verified.finish().as_ref(){return Err(Error::DownloadVerificationFailed)}
            }
            sync_directory(&destination)?;sync_directory(&parent)?;
            if !current(){return Err(Error::DownloadsConflict)}
            let tx=connection.transaction().map_err(unavailable)?;
            queue(&tx,source,&files)?;
            tx.execute("UPDATE download_folder SET path=?1 WHERE id=1",[text(&destination)?]).map_err(unavailable)?;
            tx.execute("DELETE FROM download_folder_cleanup WHERE path=?1",[text(&destination)?]).map_err(unavailable)?;
            tx.commit().map_err(unavailable)?;Ok(())
        })();
        match result{
            Ok(())=>{let _=cleanup(connection,&destination);Ok(destination)},
            Err(error)=>{let _=cleanup(connection,source);Err(error)},
        }
    }
}

#[cfg(all(test,unix))]
mod tests{
    use super::*;
    #[test]
    fn crash_recovery_cleans_only_the_recorded_staging_files_and_never_the_active_folder(){
        let root=std::env::temp_dir().join(format!("desktop-folder-recovery-{}-{}",std::process::id(),std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos()));
        let active=root.join("active");let staged=root.join("staged");fs::create_dir_all(&active).unwrap();fs::create_dir(&staged).unwrap();
        let mut connection=Connection::open_in_memory().unwrap();connection.execute_batch("PRAGMA foreign_keys=ON;").unwrap();open(&connection,&active).unwrap();
        let name=format!("{}.media","a".repeat(64));fs::write(staged.join(&name),b"staged").unwrap();fs::write(staged.join("personal.txt"),b"preserve").unwrap();
        let tx=connection.transaction().unwrap();queue(&tx,&staged,&[name.clone()]).unwrap();tx.commit().unwrap();
        assert_eq!(retained_bytes(&connection,&active).unwrap(),6);
        assert_eq!(open(&connection,&active).unwrap(),active);assert!(!staged.join(name).exists());assert_eq!(fs::read(staged.join("personal.txt")).unwrap(),b"preserve");
        assert_eq!(retained_bytes(&connection,&active).unwrap(),0);
        let name=format!("{}.part","b".repeat(64));fs::write(active.join(&name),b"active").unwrap();
        let tx=connection.transaction().unwrap();queue(&tx,&active,&[name.clone()]).unwrap();tx.commit().unwrap();
        cleanup(&connection,&active).unwrap();assert_eq!(fs::read(active.join(name)).unwrap(),b"active");
        drop(connection);fs::remove_dir_all(root).unwrap();
    }
}
