use crate::boundary::{Error, Result};
use crate::equalizer::{EqualizerChange, EqualizerSettings, SavedEqualizer};
use rusqlite::{params, Connection, OpenFlags, TransactionBehavior};
use std::path::Path;

const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS desktop_equalizer_state (
    id INTEGER PRIMARY KEY CHECK(id=1),
    revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    preamp_db REAL NOT NULL CHECK(preamp_db BETWEEN -12 AND 12 AND preamp_db*2=CAST(preamp_db*2 AS INTEGER))
) STRICT;
CREATE TABLE IF NOT EXISTS desktop_equalizer_bands (
    state_id INTEGER NOT NULL CHECK(state_id=1) REFERENCES desktop_equalizer_state(id),
    band_index INTEGER NOT NULL CHECK(band_index BETWEEN 0 AND 9),
    gain_db REAL NOT NULL CHECK(gain_db BETWEEN -12 AND 12 AND gain_db*2=CAST(gain_db*2 AS INTEGER)),
    PRIMARY KEY(state_id,band_index)
) STRICT;
";

/// Native-local normalized rows. No account/output key, serialized graph or sync transport.
pub struct EqualizerStore { connection: Connection }

impl EqualizerStore {
    /// The caller supplies a fixed app_local_data_dir path; never a path from the webview.
    pub fn open(path: &Path) -> Result<Self> {
        let parent = path.parent().ok_or(Error::PreferencesUnavailable)?;
        std::fs::create_dir_all(parent).map_err(|_| Error::PreferencesUnavailable)?;
        match std::fs::symlink_metadata(path) {
            Ok(metadata) if metadata.is_file() && !metadata.file_type().is_symlink() => {},
            Ok(_) => return Err(Error::PreferencesUnavailable),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                let mut file = std::fs::OpenOptions::new();
                file.write(true).create_new(true);
                #[cfg(unix)]
                { use std::os::unix::fs::OpenOptionsExt; file.mode(0o600); }
                file.open(path).map_err(|_| Error::PreferencesUnavailable)?;
            },
            Err(_) => return Err(Error::PreferencesUnavailable),
        }
        let flags = OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_NO_MUTEX | OpenFlags::SQLITE_OPEN_NOFOLLOW;
        Self::initialize(Connection::open_with_flags(path, flags).map_err(|_| Error::PreferencesUnavailable)?)
    }

    fn initialize(mut connection: Connection) -> Result<Self> {
        connection.busy_timeout(std::time::Duration::from_secs(2)).map_err(|_| Error::PreferencesUnavailable)?;
        connection.execute_batch("PRAGMA foreign_keys=ON; PRAGMA trusted_schema=OFF;").map_err(|_| Error::PreferencesUnavailable)?;
        let tx = connection.transaction_with_behavior(TransactionBehavior::Immediate).map_err(|_| Error::PreferencesUnavailable)?;
        tx.execute_batch(SCHEMA).map_err(|_| Error::PreferencesUnavailable)?;
        let count: u32 = tx.query_row("SELECT count(*) FROM desktop_equalizer_state", [], |row| row.get(0)).map_err(|_| Error::PreferencesUnavailable)?;
        if count == 0 {
            tx.execute("INSERT INTO desktop_equalizer_state(id,revision,enabled,preamp_db) VALUES(1,0,0,0)", []).map_err(|_| Error::PreferencesUnavailable)?;
            for index in 0..10 {
                tx.execute("INSERT INTO desktop_equalizer_bands(state_id,band_index,gain_db) VALUES(1,?1,0)", [index])
                    .map_err(|_| Error::PreferencesUnavailable)?;
            }
        }
        Self::read_from(&tx)?;
        tx.commit().map_err(|_| Error::PreferencesUnavailable)?;
        Ok(Self { connection })
    }

    fn read_from(connection: &Connection) -> Result<SavedEqualizer> {
        let (revision, enabled, preamp_db) = connection.query_row(
            "SELECT revision,enabled,preamp_db FROM desktop_equalizer_state WHERE id=1", [],
            |row| Ok((row.get::<_,i64>(0)?, row.get::<_,i32>(1)?, row.get::<_,f64>(2)?)),
        ).map_err(|_| Error::PreferencesUnavailable)?;
        if !(0..=9_007_199_254_740_991).contains(&revision) || !matches!(enabled, 0 | 1) {
            return Err(Error::PreferencesUnavailable);
        }
        let mut statement = connection.prepare("SELECT band_index,gain_db FROM desktop_equalizer_bands WHERE state_id=1 ORDER BY band_index")
            .map_err(|_| Error::PreferencesUnavailable)?;
        let rows = statement.query_map([], |row| Ok((row.get::<_,u8>(0)?, row.get::<_,f64>(1)?)))
            .map_err(|_| Error::PreferencesUnavailable)?;
        let mut bands_db = [0.0; 10];
        let mut count = 0;
        for row in rows {
            let (index, gain) = row.map_err(|_| Error::PreferencesUnavailable)?;
            let index = usize::from(index);
            if index != count || index >= 10 { return Err(Error::PreferencesUnavailable); }
            bands_db[index] = gain; count += 1;
        }
        if count != 10 { return Err(Error::PreferencesUnavailable); }
        let settings = EqualizerSettings { enabled: enabled == 1, preamp_db, bands_db };
        settings.validate().map_err(|_| Error::PreferencesUnavailable)?;
        Ok(SavedEqualizer { revision: revision as u64, settings })
    }

    pub fn read(&self) -> Result<SavedEqualizer> { Self::read_from(&self.connection) }

    /// Settings and the player's future shortcut share this exact transaction. Revisions reject
    /// a stale editor rather than letting its old copy erase a newer app-wide setting.
    pub fn change(&mut self, change: EqualizerChange, mut apply: impl FnMut(&EqualizerSettings) -> Result<()>) -> Result<SavedEqualizer> {
        let tx = self.connection.transaction_with_behavior(TransactionBehavior::Immediate).map_err(|_| Error::PreferencesUnavailable)?;
        let before = Self::read_from(&tx)?;
        if before.revision != change.expected_revision() { return Err(Error::PreferencesConflict); }
        let next = SavedEqualizer { revision: before.revision.checked_add(1).filter(|n| *n <= 9_007_199_254_740_991).ok_or(Error::PreferencesConflict)?, settings: change.target(&before.settings)? };
        apply(&next.settings)?;
        let stored = (|| -> rusqlite::Result<()> {
            tx.execute("UPDATE desktop_equalizer_state SET revision=?1,enabled=?2,preamp_db=?3 WHERE id=1", params![next.revision as i64,next.settings.enabled,next.settings.preamp_db])?;
            for (index, gain) in next.settings.bands_db.iter().enumerate() {
                tx.execute("UPDATE desktop_equalizer_bands SET gain_db=?1 WHERE state_id=1 AND band_index=?2", params![gain,index as i32])?;
            }
            tx.commit()
        })();
        if stored.is_err() {
            // The engine must not acknowledge a preference that failed to persist.
            apply(&before.settings)?;
            return Err(Error::PreferencesUnavailable);
        }
        Ok(next)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn one_setting_persists_without_output_or_account_rows_and_reset_keeps_enabled() {
        let path = std::env::temp_dir().join(format!("portico-equalizer-{}-{}.sqlite3", std::process::id(), std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos()));
        let mut store = EqualizerStore::open(&path).unwrap();
        let settings = EqualizerSettings { enabled:true, preamp_db:-3.0, bands_db:[2.0;10] };
        store.change(EqualizerChange::Set {expected_revision:0,settings:settings.clone()}, |_|Ok(())).unwrap();
        drop(store);
        let mut reopened = EqualizerStore::open(&path).unwrap();
        assert_eq!(reopened.read().unwrap().settings, settings);
        assert_eq!(reopened.change(EqualizerChange::ResetFlat {expected_revision:0}, |_|Ok(())).unwrap_err(), Error::PreferencesConflict);
        let flat = reopened.change(EqualizerChange::ResetFlat {expected_revision:1}, |_|Ok(())).unwrap();
        assert_eq!(flat.settings, EqualizerSettings {enabled:true,..Default::default()});
        let columns:Vec<String> = reopened.connection.prepare("SELECT name FROM pragma_table_info('desktop_equalizer_state')").unwrap()
            .query_map([], |row|row.get(0)).unwrap().collect::<rusqlite::Result<_>>().unwrap();
        assert_eq!(columns, ["id","revision","enabled","preamp_db"]);
        drop(reopened); std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn engine_rejection_does_not_persist_a_setting() {
        let mut store = EqualizerStore::initialize(Connection::open_in_memory().unwrap()).unwrap();
        let result = store.change(EqualizerChange::Set {expected_revision:0,settings:EqualizerSettings {enabled:true,..Default::default()}}, |_|Err(Error::EngineFailure));
        assert_eq!(result.unwrap_err(), Error::EngineFailure);
        assert_eq!(store.read().unwrap(), SavedEqualizer {revision:0,settings:EqualizerSettings::default()});
    }
    #[test]
    fn persistence_failure_restores_engine_and_rows() {
        let mut store = EqualizerStore::initialize(Connection::open_in_memory().unwrap()).unwrap();
        store.connection.execute_batch("CREATE TRIGGER reject_band BEFORE UPDATE ON desktop_equalizer_bands BEGIN SELECT RAISE(ABORT,'reject'); END;").unwrap();
        let next = EqualizerSettings {enabled:true,preamp_db:-2.0,bands_db:[1.0;10]};
        let mut applied = Vec::new();
        let result = store.change(EqualizerChange::Set {expected_revision:0,settings:next.clone()}, |settings|{applied.push(settings.clone());Ok(())});
        assert_eq!(result.unwrap_err(), Error::PreferencesUnavailable);
        assert_eq!(applied, [next,EqualizerSettings::default()]);
        assert_eq!(store.read().unwrap(), SavedEqualizer {revision:0,settings:EqualizerSettings::default()});
    }
    #[test]
    fn missing_band_is_rejected_without_repairing_persisted_rows() {
        let store = EqualizerStore::initialize(Connection::open_in_memory().unwrap()).unwrap();
        store.connection.execute("DELETE FROM desktop_equalizer_bands WHERE band_index=4", []).unwrap();
        assert_eq!(store.read().unwrap_err(), Error::PreferencesUnavailable);
        assert_eq!(store.connection.query_row("SELECT count(*) FROM desktop_equalizer_bands", [], |row|row.get::<_,i32>(0)).unwrap(),9);
    }
}
