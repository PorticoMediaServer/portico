//! App-private per-installation secret. Never derived from hardware or an account.
use crate::boundary::{Error,Result};
use ring::rand::{SecureRandom,SystemRandom};
use rusqlite::{Connection,OpenFlags,TransactionBehavior};
use std::path::Path;

pub struct InstallationStore{connection:Connection}
impl InstallationStore{
    pub fn open(path:&Path)->Result<Self>{
        let failed=|_|Error::PreferencesUnavailable;
        let parent=path.parent().ok_or(Error::PreferencesUnavailable)?;
        std::fs::create_dir_all(parent).map_err(failed)?;
        if std::fs::symlink_metadata(parent).map_err(failed)?.file_type().is_symlink(){return Err(Error::PreferencesUnavailable)}
        #[cfg(unix)]{
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(parent,std::fs::Permissions::from_mode(0o700)).map_err(failed)?;
        }
        let created=match std::fs::symlink_metadata(path){
            Ok(metadata) if metadata.is_file()&&!metadata.file_type().is_symlink()=>{
                #[cfg(unix)]{use std::os::unix::fs::PermissionsExt;if metadata.permissions().mode()&0o077!=0{return Err(Error::PreferencesUnavailable)}}
                false
            },
            Ok(_)=>return Err(Error::PreferencesUnavailable),
            Err(error) if error.kind()==std::io::ErrorKind::NotFound=>{
                let mut file=std::fs::OpenOptions::new();file.create_new(true).write(true);
                #[cfg(unix)]{use std::os::unix::fs::OpenOptionsExt;file.mode(0o600);}
                file.open(path).map_err(failed)?;true
            },
            Err(_)=>return Err(Error::PreferencesUnavailable),
        };
        let mut connection=Connection::open_with_flags(path,OpenFlags::SQLITE_OPEN_READ_WRITE|OpenFlags::SQLITE_OPEN_NO_MUTEX|OpenFlags::SQLITE_OPEN_NOFOLLOW).map_err(|_|Error::PreferencesUnavailable)?;
        connection.busy_timeout(std::time::Duration::from_secs(2)).map_err(|_|Error::PreferencesUnavailable)?;
        connection.execute_batch("PRAGMA trusted_schema=OFF; PRAGMA synchronous=FULL; PRAGMA locking_mode=EXCLUSIVE;").map_err(|_|Error::PreferencesUnavailable)?;
        let tx=connection.transaction_with_behavior(TransactionBehavior::Immediate).map_err(|_|Error::PreferencesUnavailable)?;
        if created{
            tx.execute_batch("CREATE TABLE desktop_installation(id INTEGER PRIMARY KEY CHECK(id=1),installation_id TEXT NOT NULL CHECK(length(installation_id)=64)) STRICT;").map_err(|_|Error::PreferencesUnavailable)?;
            let mut bytes=[0u8;32];SystemRandom::new().fill(&mut bytes).map_err(|_|Error::PreferencesUnavailable)?;
            let id=bytes.iter().map(|byte|format!("{byte:02x}")).collect::<String>();
            tx.execute("INSERT INTO desktop_installation VALUES(1,?1)",[id]).map_err(|_|Error::PreferencesUnavailable)?;
        }
        // Existing damage or a missing row is never replaced with a new identity.
        Self::read_from(&tx)?;tx.commit().map_err(|_|Error::PreferencesUnavailable)?;
        Ok(Self{connection})
    }
    fn read_from(connection:&Connection)->Result<String>{
        let count:u32=connection.query_row("SELECT count(*) FROM desktop_installation",[],|row|row.get(0)).map_err(|_|Error::PreferencesUnavailable)?;
        let id:String=connection.query_row("SELECT installation_id FROM desktop_installation WHERE id=1",[],|row|row.get(0)).map_err(|_|Error::PreferencesUnavailable)?;
        if count!=1||id.len()!=64||!id.bytes().all(|c|c.is_ascii_digit()||(b'a'..=b'f').contains(&c)){return Err(Error::PreferencesUnavailable)}
        Ok(id)
    }
    pub fn read(&self)->Result<String>{Self::read_from(&self.connection)}
}

#[cfg(test)]
mod tests{
    use super::*;
    fn path(label:&str)->std::path::PathBuf{std::env::temp_dir().join(format!("desktop-installation-{label}-{}-{}",std::process::id(),std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos())).join("installation.sqlite3")}
    #[test]
    fn durable_installation_identity_is_local_unique_and_never_account_or_output_keyed(){
        let first=path("first");let second=path("second");
        let store=InstallationStore::open(&first).unwrap();let original=store.read().unwrap();assert_eq!(original.len(),64);
        assert!(InstallationStore::open(&first).is_err(),"a second app cannot rotate the same credential vault concurrently");drop(store);
        assert!(InstallationStore::open(&first).unwrap().read().unwrap()==original);
        assert!(InstallationStore::open(&second).unwrap().read().unwrap()!=original);
        #[cfg(unix)]{use std::os::unix::fs::PermissionsExt;assert_eq!(std::fs::metadata(&first).unwrap().permissions().mode()&0o777,0o600);}
        std::fs::remove_dir_all(first.parent().unwrap()).unwrap();std::fs::remove_dir_all(second.parent().unwrap()).unwrap();
    }
    #[test]
    fn missing_or_damaged_record_never_silently_recreates_a_credential_identity(){
        let file=path("damage");let store=InstallationStore::open(&file).unwrap();
        store.connection.execute("DELETE FROM desktop_installation",[]).unwrap();assert!(store.read().is_err());drop(store);
        assert!(InstallationStore::open(&file).is_err());
        std::fs::remove_file(&file).unwrap();std::fs::write(&file,b"damaged database").unwrap();assert!(InstallationStore::open(&file).is_err());
        std::fs::remove_dir_all(file.parent().unwrap()).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn refuses_a_symlink_instead_of_reading_another_app_or_personal_file(){
        let file=path("link");std::fs::create_dir_all(file.parent().unwrap()).unwrap();
        let original=file.with_extension("retained");std::fs::write(&original,b"original").unwrap();std::os::unix::fs::symlink(&original,&file).unwrap();
        assert!(InstallationStore::open(&file).is_err());assert_eq!(std::fs::read(&original).unwrap(),b"original");std::fs::remove_dir_all(file.parent().unwrap()).unwrap();
    }
}
