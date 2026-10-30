//! Device-owned offline files and normalized bookkeeping. No URLs, sessions or web paths.
use crate::boundary::{Error, Result};
use crate::download_receipt::{
    DownloadViewer, ReceiptBinding, ReceiptEnvelope, SAFE_INTEGER, digest, identifier,
    verify_receipt,
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use ring::digest::{Context, SHA256};
use ring::rand::{SecureRandom, SystemRandom};
use rusqlite::{Connection, OpenFlags, OptionalExtension, Row, TransactionBehavior, params};
use serde::{Deserialize, Serialize};
use std::{
    collections::HashMap,
    fs::{File, OpenOptions},
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

const CHUNK_BYTES: usize = 256 * 1024;
const SQL: &str = "
PRAGMA foreign_keys=ON;
PRAGMA trusted_schema=OFF;
-- One native store owns the filesystem journal. Retain its SQLite write lock so
-- a second app process cannot race a file append/rename outside a SQL transaction.
PRAGMA locking_mode=EXCLUSIVE;
CREATE TABLE IF NOT EXISTS download_scopes (
 id TEXT PRIMARY KEY, authority TEXT NOT NULL CHECK(authority IN ('local','hosted')),
 account_id TEXT NOT NULL, profile_id TEXT NOT NULL, server_id TEXT NOT NULL,
 clock_floor_ms INTEGER NOT NULL CHECK(clock_floor_ms>=0),
 UNIQUE(authority,account_id,profile_id,server_id)
) STRICT;
CREATE TABLE IF NOT EXISTS download_rules (
 id INTEGER PRIMARY KEY CHECK(id=1),revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 remove_watched INTEGER NOT NULL CHECK(remove_watched IN (0,1)),
 next_episode INTEGER NOT NULL CHECK(next_episode IN (0,1)),
 unmetered_only INTEGER NOT NULL CHECK(unmetered_only IN (0,1)),
 limit_bytes INTEGER NOT NULL CHECK(limit_bytes BETWEEN 0 AND 9007199254740991)
) STRICT;
CREATE TABLE IF NOT EXISTS download_entries (
 id TEXT PRIMARY KEY,scope_id TEXT NOT NULL REFERENCES download_scopes(id),
 preparation_id TEXT NOT NULL,item_id TEXT NOT NULL,quality TEXT NOT NULL,
 sha256 TEXT NOT NULL CHECK(length(sha256)=64),expected_bytes INTEGER NOT NULL CHECK(expected_bytes>0),
 bytes_done INTEGER NOT NULL CHECK(bytes_done BETWEEN 0 AND expected_bytes),
 state TEXT NOT NULL CHECK(state IN ('queued','downloading','paused','failed','ready')),
 held INTEGER NOT NULL CHECK(held IN (0,1)),revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 9007199254740991),
 added_ms INTEGER NOT NULL CHECK(added_ms>=0),UNIQUE(scope_id,preparation_id)
) STRICT;
CREATE INDEX IF NOT EXISTS download_entries_page ON download_entries(scope_id,added_ms DESC,id);
CREATE TABLE IF NOT EXISTS download_titles (
 entry_id TEXT PRIMARY KEY REFERENCES download_entries(id) ON DELETE CASCADE,
 kind TEXT NOT NULL,title TEXT NOT NULL,artist TEXT NOT NULL,parent_id TEXT NOT NULL,parent_title TEXT NOT NULL,
 group_id TEXT NOT NULL,group_title TEXT NOT NULL,group_number INTEGER,number INTEGER,
 year INTEGER NOT NULL,duration_ms INTEGER NOT NULL,on_server INTEGER NOT NULL CHECK(on_server IN (0,1))
) STRICT;
CREATE TABLE IF NOT EXISTS download_keys (
 scope_id TEXT NOT NULL REFERENCES download_scopes(id),key_id TEXT NOT NULL,public_key TEXT NOT NULL,
 PRIMARY KEY(scope_id,key_id)
) STRICT;
CREATE TABLE IF NOT EXISTS download_receipts (
 entry_id TEXT PRIMARY KEY REFERENCES download_entries(id) ON DELETE CASCADE,
 receipt_id TEXT NOT NULL,key_id TEXT NOT NULL,payload TEXT NOT NULL,signature TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),expires_ms INTEGER NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS download_revocations (
 scope_id TEXT NOT NULL REFERENCES download_scopes(id),receipt_id TEXT NOT NULL,
 PRIMARY KEY(scope_id,receipt_id)
) STRICT;
CREATE TABLE IF NOT EXISTS download_plays (
 scope_id TEXT NOT NULL REFERENCES download_scopes(id),play_id TEXT NOT NULL,item_id TEXT NOT NULL,
 position_ms INTEGER NOT NULL CHECK(position_ms>=0),ended INTEGER NOT NULL CHECK(ended IN (0,1)),
 observed_ms INTEGER NOT NULL CHECK(observed_ms>=0),PRIMARY KEY(scope_id,play_id)
) STRICT;
CREATE TABLE IF NOT EXISTS download_local_sequence (
 id INTEGER PRIMARY KEY CHECK(id=1),value INTEGER NOT NULL CHECK(value BETWEEN 0 AND 9007199254740991)
) STRICT;
INSERT OR IGNORE INTO download_local_sequence VALUES(1,0);
CREATE INDEX IF NOT EXISTS download_plays_pending ON download_plays(scope_id,observed_ms,play_id);
CREATE TABLE IF NOT EXISTS download_garbage (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE IF NOT EXISTS download_artwork (
 entry_id TEXT PRIMARY KEY REFERENCES download_entries(id) ON DELETE CASCADE,
 mime TEXT NOT NULL CHECK(mime IN ('image/jpeg','image/png','image/webp')),
 bytes BLOB NOT NULL CHECK(length(bytes) BETWEEN 1 AND 4194304)
) STRICT;
";

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DownloadRules {
    pub remove_watched: bool,
    pub next_episode: bool,
    pub unmetered_only: bool,
    pub limit_bytes: u64,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RulesSnapshot {
    pub revision: u64,
    pub rules: DownloadRules,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DownloadTitle {
    pub kind: String,
    pub title: String,
    pub artist: String,
    pub parent_id: String,
    pub parent_title: String,
    pub group_id: String,
    pub group_title: String,
    pub group_number: Option<u32>,
    pub number: Option<u32>,
    pub year: u32,
    pub duration_ms: u64,
    pub on_server: bool,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Enrollment {
    pub viewer: DownloadViewer,
    pub preparation_id: String,
    pub item_id: String,
    pub quality: String,
    pub sha256: String,
    pub bytes: u64,
    pub title: DownloadTitle,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LocalDownload {
    pub id: String,
    pub preparation_id: String,
    pub item_id: String,
    pub quality: String,
    pub sha256: String,
    pub bytes: u64,
    pub bytes_done: u64,
    pub state: String,
    pub held: bool,
    pub revision: u64,
    pub added_ms: i64,
    pub receipt_expires_ms: Option<i64>,
    /// Derived by the authority-bound IPC projection, never inferred from ready/expiry.
    pub offline_access: String,
    pub title: DownloadTitle,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DownloadCursor {
    pub added_ms: i64,
    pub id: String,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LocalDownloadPage {
    pub items: Vec<LocalDownload>,
    pub next_cursor: Option<DownloadCursor>,
}
#[derive(Debug, Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct PendingPlay {
    pub play_id: String,
    pub item_id: String,
    pub position_ms: u64,
    pub ended: bool,
    pub observed_ms: i64,
}
#[derive(Clone,Debug,Serialize,PartialEq,Eq)]
pub struct PlaybackArtifact {pub sha256:String,pub bytes:u64}
/// Private path stays native. Only the signed/verified artifact is serialized.
pub struct VerifiedPlayback {pub path:PathBuf,pub artifact:PlaybackArtifact}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DownloadCapacity {
    pub total_bytes: u64,
    pub free_bytes: u64,
    pub used_bytes: u64,
    pub other_downloads_bytes: u64,
}
#[derive(Debug, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DownloadGroup {
    pub kind: String,
    pub parent_id: String,
    pub group_id: String,
    pub count: u64,
    pub revision: String,
    pub bytes: u64,
    pub bytes_done: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub parent_title: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub artist: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub quality: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub held: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub artwork_id: Option<String>,
    pub offline_access: String,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct GroupSelector {
    pub kind: String,
    pub parent_id: String,
    pub group_id: Option<String>,
}
impl GroupSelector {
    fn validate(&self) -> Result<()> {
        if !matches!(self.kind.as_str(), "episodes" | "songs")
            || !identifier(&self.parent_id)
            || self
                .group_id
                .as_ref()
                .is_some_and(|v| !v.is_empty() && !identifier(v))
        {
            return Err(Error::InvalidCommand);
        }
        Ok(())
    }
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum GroupAction {
    Remove,
    Pause,
    Resume,
    Retry,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct OtherDownloadsPreview {
    pub snapshot_token: String,
    pub bytes: u64,
}
#[derive(Debug, PartialEq)]
struct RemovalTarget {
    id: String,
    revision: u64,
    bytes: u64,
}
struct RemovalSnapshot {
    viewer: DownloadViewer,
    targets: Vec<RemovalTarget>,
    created: Instant,
}
#[derive(Debug, Serialize)]
pub struct DownloadArtwork {
    pub mime: String,
    pub bytes: Vec<u8>,
}
pub(super) fn disk_space(path: &Path) -> Result<(u64, u64)> {
    #[cfg(unix)]
    {
        use std::{ffi::CString, os::unix::ffi::OsStrExt};
        let path = CString::new(path.as_os_str().as_bytes()).map_err(unavailable)?;
        let mut value = std::mem::MaybeUninit::<libc::statvfs>::uninit();
        if unsafe { libc::statvfs(path.as_ptr(), value.as_mut_ptr()) } != 0 {
            return Err(Error::DownloadsUnavailable);
        }
        let value = unsafe { value.assume_init() };
        let block = value.f_frsize as u64;
        return Ok((
            (value.f_blocks as u64)
                .checked_mul(block)
                .ok_or(Error::DownloadsUnavailable)?,
            (value.f_bavail as u64)
                .checked_mul(block)
                .ok_or(Error::DownloadsUnavailable)?,
        ));
    }
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        #[link(name = "kernel32")]
        unsafe extern "system" {
            fn GetDiskFreeSpaceExW(
                path: *const u16,
                available: *mut u64,
                total: *mut u64,
                free: *mut u64,
            ) -> i32;
        }
        let path = path
            .as_os_str()
            .encode_wide()
            .chain(std::iter::once(0))
            .collect::<Vec<_>>();
        let (mut available, mut total, mut free) = (0, 0, 0);
        if unsafe { GetDiskFreeSpaceExW(path.as_ptr(), &mut available, &mut total, &mut free) } == 0
        {
            return Err(Error::DownloadsUnavailable);
        }
        return Ok((total, available));
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = path;
        Err(Error::Unsupported)
    }
}

fn unavailable<T>(_: T) -> Error {
    Error::DownloadsUnavailable
}
fn now_ms() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis().min(i64::MAX as u128) as i64)
        .unwrap_or(0)
}
fn hash(bytes: &[u8]) -> String {
    hex(ring::digest::digest(&SHA256, bytes).as_ref())
}
fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}
fn scope(viewer: &DownloadViewer) -> Result<String> {
    viewer.validate()?;
    Ok(hash(
        format!(
            "{}\0{}\0{}\0{}",
            viewer.authority, viewer.account_id, viewer.profile_id, viewer.server_id
        )
        .as_bytes(),
    ))
}
fn title_valid(title: &DownloadTitle) -> bool {
    [
        &title.kind,
        &title.title,
        &title.artist,
        &title.parent_title,
        &title.group_title,
    ]
    .iter()
    .all(|v| v.len() <= 1024 && !v.chars().any(char::is_control))
        && [&title.parent_id, &title.group_id]
            .iter()
            .all(|v| v.is_empty() || identifier(v))
        && title.duration_ms <= SAFE_INTEGER
        && title.year <= 9999
}
fn private_dir(path: &Path) -> Result<()> {
    match std::fs::symlink_metadata(path) {
        Ok(meta) if meta.is_dir() && !meta.file_type().is_symlink() => Ok(()),
        Ok(_) => Err(Error::DownloadsUnavailable),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            let mut builder = std::fs::DirBuilder::new();
            builder.recursive(true);
            #[cfg(unix)]
            {
                use std::os::unix::fs::DirBuilderExt;
                builder.mode(0o700);
            }
            builder.create(path).map_err(unavailable)
        }
        Err(_) => Err(Error::DownloadsUnavailable),
    }
}
pub(super) fn open_file(path: &Path, write: bool, create: bool) -> Result<File> {
    let mut options = OpenOptions::new();
    options.read(true).write(write).create_new(create);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;
        options.custom_flags(0x0020_0000);
    }
    let file = options.open(path).map_err(unavailable)?;
    let metadata = file.metadata().map_err(unavailable)?;
    if !metadata.is_file() {
        return Err(Error::DownloadsUnavailable);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(Error::DownloadsUnavailable);
        }
    }
    Ok(file)
}

const READ_ENTRY: &str = "SELECT e.id,e.preparation_id,e.item_id,e.quality,e.sha256,e.expected_bytes,e.bytes_done,e.state,e.held,e.revision,e.added_ms,r.expires_ms,t.kind,t.title,t.artist,t.parent_id,t.parent_title,t.group_id,t.group_title,t.group_number,t.number,t.year,t.duration_ms,t.on_server FROM download_entries e JOIN download_titles t ON t.entry_id=e.id LEFT JOIN download_receipts r ON r.entry_id=e.id ";
fn count(row: &Row<'_>, index: usize) -> rusqlite::Result<u64> {
    let value = row.get::<_, i64>(index)?;
    if value < 0 || value as u64 > SAFE_INTEGER {
        return Err(rusqlite::Error::IntegralValueOutOfRange(index, value));
    }
    Ok(value as u64)
}
fn row_entry(row: &Row<'_>) -> rusqlite::Result<LocalDownload> {
    Ok(LocalDownload {
        id: row.get(0)?,
        preparation_id: row.get(1)?,
        item_id: row.get(2)?,
        quality: row.get(3)?,
        sha256: row.get(4)?,
        bytes: count(row, 5)?,
        bytes_done: count(row, 6)?,
        state: row.get(7)?,
        held: row.get(8)?,
        revision: count(row, 9)?,
        added_ms: row.get(10)?,
        receipt_expires_ms: row.get(11)?,
        offline_access: "needsCheckIn".into(),
        title: DownloadTitle {
            kind: row.get(12)?,
            title: row.get(13)?,
            artist: row.get(14)?,
            parent_id: row.get(15)?,
            parent_title: row.get(16)?,
            group_id: row.get(17)?,
            group_title: row.get(18)?,
            group_number: row.get(19)?,
            number: row.get(20)?,
            year: row.get(21)?,
            duration_ms: count(row, 22)?,
            on_server: row.get(23)?,
        },
    })
}

pub struct DownloadStore {
    connection: Connection,
    metadata_root:PathBuf,
    files: PathBuf,
    previews: HashMap<String, RemovalSnapshot>,
}
impl DownloadStore {
    /// Fixed native app-data root, not a path accepted from IPC. Initial rules are supplied by
    /// the authority-bound adapter only on first creation; reopening never overwrites rules.
    pub fn open(root: &Path, initial: &DownloadRules) -> Result<Self> {
        if initial.limit_bytes > SAFE_INTEGER {
            return Err(Error::InvalidCommand);
        }
        private_dir(root)?;
        let files = root.join("files");
        private_dir(&files)?;
        let db = root.join("downloads.sqlite3");
        if !db.exists() {
            open_file(&db, true, true)?;
        }
        let flags = OpenFlags::SQLITE_OPEN_READ_WRITE
            | OpenFlags::SQLITE_OPEN_NO_MUTEX
            | OpenFlags::SQLITE_OPEN_NOFOLLOW;
        let connection = Connection::open_with_flags(db, flags).map_err(unavailable)?;
        connection
            .busy_timeout(std::time::Duration::from_secs(2))
            .map_err(unavailable)?;
        connection.execute_batch(SQL).map_err(unavailable)?;
        let files=crate::download_folder::open(&connection,&files)?;
        connection
            .execute(
                "INSERT OR IGNORE INTO download_rules VALUES(1,0,?1,?2,?3,?4)",
                params![
                    initial.remove_watched,
                    initial.next_episode,
                    initial.unmetered_only,
                    initial.limit_bytes as i64
                ],
            )
            .map_err(unavailable)?;
        let store = Self {
            connection,
            metadata_root:root.to_path_buf(),
            files,
            previews: HashMap::new(),
        };
        store.collect_garbage()?;
        store.recover()?;
        Ok(store)
    }
    /// The only native directory that may be shown deliberately as Folder in Saved.
    pub fn folder_path(&self)->&Path{&self.files}
    pub fn relocate_files(&mut self,selected:&Path,current:impl Fn()->bool)->Result<()>{
        self.files=crate::download_folder::relocate(&mut self.connection,&self.files,selected,current)?;
        self.previews.clear();Ok(())
    }
    fn ensure_scope(&self, viewer: &DownloadViewer) -> Result<String> {
        let id = scope(viewer)?;
        self.connection
            .execute(
                "INSERT OR IGNORE INTO download_scopes VALUES(?1,?2,?3,?4,?5,0)",
                params![
                    id,
                    viewer.authority,
                    viewer.account_id,
                    viewer.profile_id,
                    viewer.server_id
                ],
            )
            .map_err(unavailable)?;
        Ok(id)
    }
    fn path(&self, id: &str, ready: bool) -> Result<PathBuf> {
        if !digest(id) {
            return Err(Error::InvalidCommand);
        }
        let metadata=std::fs::symlink_metadata(&self.files).map_err(unavailable)?;
        if !metadata.is_dir()||metadata.file_type().is_symlink(){return Err(Error::DownloadsUnavailable)}
        Ok(self
            .files
            .join(format!("{id}.{}", if ready { "media" } else { "part" })))
    }
    fn recover(&self) -> Result<()> {
        let mut query = self
            .connection
            .prepare("SELECT id,bytes_done FROM download_entries WHERE state!='ready'")
            .map_err(unavailable)?;
        let rows = query
            .query_map([], |r| Ok((r.get::<_, String>(0)?, count(r, 1)?)))
            .map_err(unavailable)?;
        for row in rows {
            let (id, committed) = row.map_err(unavailable)?;
            let partial = self.path(&id, false)?;
            let sealed = self.path(&id, true)?;
            let retry = self.files.join(format!("{id}.retry"));
            if retry.exists() {
                if committed > 0 && !partial.exists() {
                    std::fs::rename(&retry, &partial).map_err(unavailable)?;
                } else {
                    std::fs::remove_file(&retry).map_err(unavailable)?;
                }
            }
            // A crash after rename but before the ready transaction never grants playback.
            if sealed.exists() && !partial.exists() {
                std::fs::rename(&sealed, &partial).map_err(unavailable)?;
            }
            let length = if partial.exists() {
                let file = open_file(&partial, true, false)?;
                let length = file.metadata().map_err(unavailable)?.len().min(committed);
                file.set_len(length).map_err(unavailable)?;
                file.sync_all().map_err(unavailable)?;
                length
            } else {
                0
            };
            self.connection.execute("UPDATE download_entries SET bytes_done=?1,state=CASE WHEN held=1 THEN 'paused' ELSE 'queued' END,revision=revision+1 WHERE id=?2",params![length as i64,id]).map_err(unavailable)?;
        }
        Ok(())
    }
    pub fn rules(&self) -> Result<RulesSnapshot> {
        self.connection.query_row("SELECT revision,remove_watched,next_episode,unmetered_only,limit_bytes FROM download_rules WHERE id=1",[],|r|Ok(RulesSnapshot{revision:count(r,0)?,rules:DownloadRules{remove_watched:r.get(1)?,next_episode:r.get(2)?,unmetered_only:r.get(3)?,limit_bytes:count(r,4)?}})).map_err(unavailable)
    }
    pub fn set_rules(&mut self, expected: u64, rules: DownloadRules) -> Result<RulesSnapshot> {
        if expected >= SAFE_INTEGER || rules.limit_bytes > SAFE_INTEGER {
            return Err(Error::InvalidCommand);
        }
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        if tx.execute("UPDATE download_rules SET revision=revision+1,remove_watched=?1,next_episode=?2,unmetered_only=?3,limit_bytes=?4 WHERE id=1 AND revision=?5",params![rules.remove_watched,rules.next_episode,rules.unmetered_only,rules.limit_bytes as i64,expected as i64]).map_err(unavailable)?!=1{return Err(Error::DownloadsConflict);}
        tx.commit().map_err(unavailable)?;
        self.rules()
    }
    pub fn enroll(&mut self, input: Enrollment) -> Result<LocalDownload> {
        let owner = self.ensure_scope(&input.viewer)?;
        if !identifier(&input.preparation_id)
            || !identifier(&input.item_id)
            || !identifier(&input.quality)
            || !digest(&input.sha256)
            || input.bytes == 0
            || input.bytes > SAFE_INTEGER
            || !title_valid(&input.title)
        {
            return Err(Error::InvalidCommand);
        }
        let id = hash(format!("{owner}\0{}", input.preparation_id).as_bytes());
        if let Ok(old) = self.entry(&input.viewer, &id) {
            return if old.sha256 == input.sha256
                && old.bytes == input.bytes
                && old.item_id == input.item_id
                && old.quality == input.quality
            {
                Ok(old)
            } else {
                Err(Error::DownloadsConflict)
            };
        }
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        let (limit,reserved):(u64,u64)=tx.query_row("SELECT limit_bytes,(SELECT coalesce(sum(expected_bytes),0) FROM download_entries) FROM download_rules WHERE id=1",[],|r|Ok((count(r,0)?,count(r,1)?))).map_err(unavailable)?;
        if limit > 0
            && reserved
                .checked_add(input.bytes)
                .filter(|n| *n <= limit)
                .is_none()
        {
            return Err(Error::StorageFull);
        }
        if input.bytes > disk_space(&self.files)?.1 {
            return Err(Error::StorageFull);
        }
        tx.execute(
            "INSERT INTO download_entries VALUES(?1,?2,?3,?4,?5,?6,?7,0,'queued',0,0,?8)",
            params![
                id,
                owner,
                input.preparation_id,
                input.item_id,
                input.quality,
                input.sha256,
                input.bytes as i64,
                now_ms()
            ],
        )
        .map_err(unavailable)?;
        let t = input.title;
        tx.execute(
            "INSERT INTO download_titles VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,?12,?13)",
            params![
                id,
                t.kind,
                t.title,
                t.artist,
                t.parent_id,
                t.parent_title,
                t.group_id,
                t.group_title,
                t.group_number,
                t.number,
                t.year,
                t.duration_ms as i64,
                t.on_server
            ],
        )
        .map_err(unavailable)?;
        tx.commit().map_err(unavailable)?;
        self.entry(&input.viewer, &id)
    }
    pub fn entry(&self, viewer: &DownloadViewer, id: &str) -> Result<LocalDownload> {
        if !digest(id) {
            return Err(Error::InvalidCommand);
        }
        self.connection
            .query_row(
                &(READ_ENTRY.to_owned() + "WHERE e.scope_id=?1 AND e.id=?2"),
                params![scope(viewer)?, id],
                row_entry,
            )
            .map_err(unavailable)
    }
    pub fn list(
        &self,
        viewer: &DownloadViewer,
        cursor: Option<DownloadCursor>,
        limit: u32,
    ) -> Result<LocalDownloadPage> {
        if limit == 0 || limit > 200 {
            return Err(Error::InvalidCommand);
        }
        let (time, id) = match cursor {
            Some(v) if v.added_ms >= 0 && digest(&v.id) => (v.added_ms, v.id),
            Some(_) => return Err(Error::InvalidCommand),
            None => (i64::MAX, String::new()),
        };
        let sql = READ_ENTRY.to_owned()
            + "WHERE e.scope_id=?1 AND (e.added_ms<?2 OR (e.added_ms=?2 AND e.id>?3)) ORDER BY e.added_ms DESC,e.id LIMIT ?4";
        let mut statement = self.connection.prepare(&sql).map_err(unavailable)?;
        let mut items = statement
            .query_map(params![scope(viewer)?, time, id, limit + 1], row_entry)
            .map_err(unavailable)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(unavailable)?;
        let next_cursor = if items.len() > limit as usize {
            items.pop();
            items.last().map(|v| DownloadCursor {
                added_ms: v.added_ms,
                id: v.id.clone(),
            })
        } else {
            None
        };
        Ok(LocalDownloadPage { items, next_cursor })
    }
    pub fn append(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
        offset: u64,
        bytes: &[u8],
    ) -> Result<LocalDownload> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        if entry.state == "ready"
            || entry.held
            || bytes.is_empty()
            || bytes.len() > CHUNK_BYTES
            || offset != entry.bytes_done
            || offset
                .checked_add(bytes.len() as u64)
                .filter(|n| *n <= entry.bytes)
                .is_none()
        {
            return Err(Error::InvalidCommand);
        }
        let path = self.path(id, false)?;
        let mut file = open_file(&path, true, !path.exists())?;
        if file.metadata().map_err(unavailable)?.len() < offset {
            return Err(Error::DownloadsConflict);
        }
        file.set_len(offset).map_err(unavailable)?;
        file.seek(SeekFrom::Start(offset)).map_err(unavailable)?;
        file.write_all(bytes).map_err(unavailable)?;
        file.sync_data().map_err(unavailable)?;
        let count=self.connection.execute("UPDATE download_entries SET bytes_done=?1,state='downloading',revision=revision+1 WHERE id=?2 AND revision=?3",params![(offset+bytes.len() as u64) as i64,id,expected_revision as i64]).map_err(unavailable)?;
        if count != 1 {
            file.set_len(offset).map_err(unavailable)?;
            return Err(Error::DownloadsConflict);
        }
        self.entry(viewer, id)
    }
    pub fn seal(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
    ) -> Result<LocalDownload> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        if entry.held || entry.state == "ready" || entry.bytes_done != entry.bytes {
            return Err(Error::DownloadNotReady);
        }
        let partial = self.path(id, false)?;
        let sealed = self.path(id, true)?;
        let mut file = open_file(&partial, true, false)?;
        if file.metadata().map_err(unavailable)?.len() != entry.bytes {
            return Err(Error::DownloadNotReady);
        }
        let mut hash = Context::new(&SHA256);
        let mut buffer = [0u8; CHUNK_BYTES];
        loop {
            let count = file.read(&mut buffer).map_err(unavailable)?;
            if count == 0 {
                break;
            }
            hash.update(&buffer[..count]);
        }
        if hex(hash.finish().as_ref()) != entry.sha256 {
            self.connection
                .execute(
                    "UPDATE download_entries SET state='failed',revision=revision+1 WHERE id=?1",
                    [id],
                )
                .map_err(unavailable)?;
            return Err(Error::DownloadVerificationFailed);
        }
        file.sync_all().map_err(unavailable)?;
        drop(file);
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        if sealed.exists() {
            return Err(Error::DownloadsConflict);
        }
        std::fs::rename(&partial, &sealed).map_err(unavailable)?;
        let stored = (|| -> Result<()> {
            if tx.execute("UPDATE download_entries SET state='ready',revision=revision+1 WHERE id=?1 AND revision=?2",params![id,expected_revision as i64]).map_err(unavailable)?!=1{return Err(Error::DownloadsConflict);}
            tx.commit().map_err(unavailable)
        })();
        if let Err(error) = stored {
            std::fs::rename(&sealed, &partial).map_err(unavailable)?;
            return Err(error);
        }
        self.entry(viewer, id)
    }
    pub fn hold(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
        held: bool,
    ) -> Result<LocalDownload> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        if entry.state == "ready" {
            return Err(Error::InvalidCommand);
        }
        if self.connection.execute("UPDATE download_entries SET held=?1,state=?2,revision=revision+1 WHERE id=?3 AND revision=?4",params![held,if held{"paused"}else{"queued"},id,expected_revision as i64]).map_err(unavailable)?!=1{return Err(Error::DownloadsConflict)}
        self.entry(viewer, id)
    }
    pub fn retry(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
    ) -> Result<LocalDownload> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        if entry.state != "failed" {
            return Err(Error::InvalidCommand);
        }
        let partial = self.path(id, false)?;
        let backup = self.files.join(format!("{id}.retry"));
        if backup.exists() {
            return Err(Error::DownloadsConflict);
        }
        open_file(&partial, false, false)?;
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        std::fs::rename(&partial, &backup).map_err(unavailable)?;
        let result = (|| {
            if tx.execute("UPDATE download_entries SET bytes_done=0,held=0,state='queued',revision=revision+1 WHERE id=?1 AND revision=?2",params![id,expected_revision as i64]).map_err(unavailable)?!=1{return Err(Error::DownloadsConflict)}
            tx.commit().map_err(unavailable)
        })();
        if let Err(error) = result {
            std::fs::rename(&backup, &partial).map_err(unavailable)?;
            return Err(error);
        }
        std::fs::remove_file(backup).map_err(unavailable)?;
        self.entry(viewer, id)
    }
    /// Authenticated JS transport installs the server's receipt key, independently of a receipt.
    /// A key id cannot silently change its public key; a rotation needs a new key id.
    pub fn install_receipt_key(
        &self,
        viewer: &DownloadViewer,
        key_id: &str,
        public_key: &str,
    ) -> Result<()> {
        let owner = self.ensure_scope(viewer)?;
        if !identifier(key_id)
            || public_key.len() > 128
            || URL_SAFE_NO_PAD
                .decode(public_key)
                .map(|v| v.len() != 32)
                .unwrap_or(true)
        {
            return Err(Error::InvalidReceipt);
        }
        self.connection
            .execute(
                "INSERT OR IGNORE INTO download_keys VALUES(?1,?2,?3)",
                params![owner, key_id, public_key],
            )
            .map_err(unavailable)?;
        let saved: String = self
            .connection
            .query_row(
                "SELECT public_key FROM download_keys WHERE scope_id=?1 AND key_id=?2",
                params![owner, key_id],
                |r| r.get(0),
            )
            .map_err(unavailable)?;
        if saved != public_key {
            return Err(Error::InvalidReceipt);
        }
        Ok(())
    }
    fn receipt_time(&self, owner: &str) -> Result<i64> {
        self.connection
            .execute(
                "UPDATE download_scopes SET clock_floor_ms=max(clock_floor_ms,?1) WHERE id=?2",
                params![now_ms(), owner],
            )
            .map_err(unavailable)?;
        self.connection
            .query_row(
                "SELECT clock_floor_ms FROM download_scopes WHERE id=?1",
                [owner],
                |r| r.get(0),
            )
            .map_err(unavailable)
    }
    fn check_receipt(
        &self,
        viewer: &DownloadViewer,
        entry: &LocalDownload,
        receipt: &ReceiptEnvelope,
    ) -> Result<i64> {
        let owner = scope(viewer)?;
        let revoked:bool=self.connection.query_row("SELECT EXISTS(SELECT 1 FROM download_revocations WHERE scope_id=?1 AND receipt_id=?2)",params![owner,receipt.receipt_id],|r|r.get(0)).map_err(unavailable)?;
        if revoked {
            return Err(Error::InvalidReceipt);
        }
        let key: String = self
            .connection
            .query_row(
                "SELECT public_key FROM download_keys WHERE scope_id=?1 AND key_id=?2",
                params![owner, receipt.key_id],
                |r| r.get(0),
            )
            .map_err(|_| Error::InvalidReceipt)?;
        let verified = verify_receipt(
            receipt,
            &key,
            &ReceiptBinding {
                viewer,
                item_id: &entry.item_id,
                preparation_id: &entry.preparation_id,
                quality: &entry.quality,
                sha256: &entry.sha256,
                bytes: entry.bytes,
            },
            self.receipt_time(&owner)?,
        )?;
        self.connection
            .execute(
                "UPDATE download_scopes SET clock_floor_ms=max(clock_floor_ms,?1) WHERE id=?2",
                params![verified.issued_ms, owner],
            )
            .map_err(unavailable)?;
        Ok(verified.expires_ms)
    }
    pub fn save_receipt(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        receipt: ReceiptEnvelope,
    ) -> Result<()> {
        let entry = self.entry(viewer, id)?;
        let expires = self.check_receipt(viewer, &entry, &receipt)?;
        let old: Option<(String,u64,String,String)> = self
            .connection
            .query_row(
                "SELECT receipt_id,revision,payload,signature FROM download_receipts WHERE entry_id=?1",
                [id],
                |r| Ok((r.get(0)?,count(r,1)?,r.get(2)?,r.get(3)?)),
            )
            .optional()
            .map_err(unavailable)?;
        if let Some((id, revision, payload, signature)) = old {
            if id == receipt.receipt_id {
                if revision == receipt.revision
                    && payload == receipt.payload
                    && signature == receipt.signature
                {
                    return Ok(());
                }
                if revision >= receipt.revision {
                    return Err(Error::DownloadsConflict);
                }
            }
        }
        self.connection.execute("INSERT INTO download_receipts VALUES(?1,?2,?3,?4,?5,?6,?7) ON CONFLICT(entry_id) DO UPDATE SET receipt_id=excluded.receipt_id,key_id=excluded.key_id,payload=excluded.payload,signature=excluded.signature,revision=excluded.revision,expires_ms=excluded.expires_ms",params![id,receipt.receipt_id,receipt.key_id,receipt.payload,receipt.signature,receipt.revision as i64,expires]).map_err(unavailable)?;
        Ok(())
    }
    pub fn revoke(&mut self, viewer: &DownloadViewer, receipt_id: &str) -> Result<()> {
        if !identifier(receipt_id) {
            return Err(Error::InvalidCommand);
        }
        self.connection
            .execute(
                "INSERT OR IGNORE INTO download_revocations VALUES(?1,?2)",
                params![self.ensure_scope(viewer)?, receipt_id],
            )
            .map_err(unavailable)?;
        let entry_id:Option<String>=self.connection.query_row("SELECT r.entry_id FROM download_receipts r JOIN download_entries e ON e.id=r.entry_id WHERE e.scope_id=?1 AND r.receipt_id=?2",params![scope(viewer)?,receipt_id],|r|r.get(0)).optional().map_err(unavailable)?;
        if let Some(id)=entry_id{let entry=self.entry(viewer,&id)?;self.remove(viewer,&id,entry.revision)?;}
        Ok(())
    }
    /// Native caller passes this path directly to the constrained offline mpv load. Never an IPC
    /// result. A complete file without this viewer's valid signed receipt is still not playable.
    pub fn playback_file(&self, viewer: &DownloadViewer, id: &str) -> Result<PathBuf> {
        let entry = self.entry(viewer, id)?;
        if entry.state != "ready" || entry.bytes_done != entry.bytes {
            return Err(Error::DownloadNotReady);
        }
        let receipt=self.connection.query_row("SELECT receipt_id,key_id,payload,signature,revision FROM download_receipts WHERE entry_id=?1",[id],|r|Ok(ReceiptEnvelope{receipt_id:r.get(0)?,key_id:r.get(1)?,algorithm:"ed25519".into(),payload:r.get(2)?,signature:r.get(3)?,revision:count(r,4)?})).map_err(|_|Error::InvalidReceipt)?;
        self.check_receipt(viewer, &entry, &receipt)?;
        let path = self.path(id, true)?;
        let file = open_file(&path, false, false)?;
        if file.metadata().map_err(unavailable)?.len() != entry.bytes {
            return Err(Error::DownloadVerificationFailed);
        }
        Ok(path)
    }
    pub fn verified_playback(&self,viewer:&DownloadViewer,id:&str)->Result<VerifiedPlayback>{
        // playback_file revalidates the actual viewer's signature, expiry, revocation,
        // ready row and sealed file. Rehash on native load so same-size damage cannot
        // be reported as verified artifact facts. Listing does not hash media files.
        let path=self.playback_file(viewer,id)?;
        let entry=self.entry(viewer,id)?;
        let mut file=open_file(&path,false,false)?;
        let mut hash=Context::new(&SHA256);let mut bytes=0u64;let mut buffer=[0u8;CHUNK_BYTES];
        loop{
            let count=file.read(&mut buffer).map_err(unavailable)?;if count==0{break}
            bytes=bytes.checked_add(count as u64).filter(|size|*size<=entry.bytes).ok_or(Error::DownloadVerificationFailed)?;
            hash.update(&buffer[..count]);
        }
        let sha256=hex(hash.finish().as_ref());
        if bytes!=entry.bytes||sha256!=entry.sha256{return Err(Error::DownloadVerificationFailed)}
        Ok(VerifiedPlayback{path,artifact:PlaybackArtifact{sha256,bytes}})
    }
    pub fn next_local_sequence(&self)->Result<u64>{
        self.connection.query_row("UPDATE download_local_sequence SET value=value+1 WHERE id=1 AND value<9007199254740991 RETURNING value",[],|row|count(row,0)).map_err(unavailable)
    }
    pub fn project_access(&self, viewer: &DownloadViewer, entry: &mut LocalDownload) {
        entry.offline_access = if self.playback_file(viewer, &entry.id).is_ok() {
            "valid"
        } else {
            "needsCheckIn"
        }.into();
    }
    pub fn note_play(
        &mut self,
        viewer: &DownloadViewer,
        entry_id: &str,
        play: PendingPlay,
    ) -> Result<()> {
        let entry = self.entry(viewer, entry_id)?;
        if play.item_id != entry.item_id
            || !identifier(&play.play_id)
            || play.position_ms > SAFE_INTEGER
            || play.observed_ms < 0
            || play.observed_ms > now_ms() + 300_000
        {
            return Err(Error::InvalidCommand);
        }
        let owner = scope(viewer)?;
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        tx.execute("INSERT INTO download_plays VALUES(?1,?2,?3,?4,?5,?6) ON CONFLICT(scope_id,play_id) DO UPDATE SET position_ms=excluded.position_ms,ended=max(download_plays.ended,excluded.ended),observed_ms=excluded.observed_ms WHERE download_plays.item_id=excluded.item_id AND excluded.observed_ms>download_plays.observed_ms",params![owner,play.play_id,play.item_id,play.position_ms as i64,play.ended,play.observed_ms]).map_err(unavailable)?;
        tx.execute("DELETE FROM download_plays WHERE scope_id=?1 AND play_id IN (SELECT play_id FROM download_plays WHERE scope_id=?1 ORDER BY observed_ms DESC,play_id LIMIT -1 OFFSET 400)",[owner]).map_err(unavailable)?;
        tx.commit().map_err(unavailable)
    }
    pub fn pending_plays(&self, viewer: &DownloadViewer) -> Result<Vec<PendingPlay>> {
        let mut query=self.connection.prepare("SELECT play_id,item_id,position_ms,ended,observed_ms FROM download_plays WHERE scope_id=?1 ORDER BY observed_ms,play_id LIMIT 100").map_err(unavailable)?;
        query
            .query_map([scope(viewer)?], |r| {
                Ok(PendingPlay {
                    play_id: r.get(0)?,
                    item_id: r.get(1)?,
                    position_ms: count(r, 2)?,
                    ended: r.get(3)?,
                    observed_ms: r.get(4)?,
                })
            })
            .map_err(unavailable)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(unavailable)
    }
    /// A newer local observation arriving during the HTTP request survives the old response.
    pub fn acknowledge_play(
        &self,
        viewer: &DownloadViewer,
        play_id: &str,
        observed_ms: i64,
    ) -> Result<()> {
        if !identifier(play_id) || observed_ms < 0 {
            return Err(Error::InvalidCommand);
        }
        self.connection
            .execute(
                "DELETE FROM download_plays WHERE scope_id=?1 AND play_id=?2 AND observed_ms=?3",
                params![scope(viewer)?, play_id, observed_ms],
            )
            .map_err(unavailable)?;
        Ok(())
    }
    pub fn usage(&self, viewer: &DownloadViewer) -> Result<(u64, u64)> {
        self.connection.query_row("SELECT coalesce(sum(CASE WHEN scope_id=?1 THEN bytes_done ELSE 0 END),0),coalesce(sum(CASE WHEN scope_id!=?1 THEN bytes_done ELSE 0 END),0) FROM download_entries",[scope(viewer)?],|r|Ok((count(r,0)?,count(r,1)?))).map_err(unavailable)
    }
    pub fn capacity(&self, viewer: &DownloadViewer) -> Result<DownloadCapacity> {
        #[cfg(unix)]{
            use std::os::unix::fs::MetadataExt;
            // The scalar UI has one volume denominator. Do not classify metadata
            // on another volume as space used on the chosen download volume.
            if std::fs::metadata(&self.files).map_err(unavailable)?.dev()!=std::fs::metadata(&self.metadata_root).map_err(unavailable)?.dev(){return Err(Error::Unsupported)}
        }
        let (total_bytes, free_bytes) = disk_space(&self.files)?;
        viewer.validate()?;
        let other_downloads_bytes = self
            .other_targets(viewer)?
            .iter()
            .try_fold(0u64, |sum, t| {
                sum.checked_add(t.bytes).ok_or(Error::DownloadsUnavailable)
            })?;
        // Count actual Portico-owned files, including metadata/artwork in SQLite and
        // pending garbage. Do not label all other accounts' bytes as removable.
        let mut used_bytes = crate::download_folder::retained_bytes(&self.connection,&self.files)?;
        for entry in std::fs::read_dir(&self.files).map_err(unavailable)? {
            let entry = entry.map_err(unavailable)?;
            let name = entry.file_name();
            let Some(name) = name.to_str() else { continue };
            let Some((id, extension)) = name.rsplit_once('.') else {
                continue;
            };
            if digest(id) && matches!(extension, "part" | "media" | "retry") {
                let meta = std::fs::symlink_metadata(entry.path()).map_err(unavailable)?;
                if meta.is_file() && !meta.file_type().is_symlink() {
                    used_bytes = used_bytes
                        .checked_add(meta.len())
                        .ok_or(Error::DownloadsUnavailable)?;
                }
            }
        }
        for name in [
            "downloads.sqlite3",
            "downloads.sqlite3-wal",
            "downloads.sqlite3-shm",
            "downloads.sqlite3-journal",
        ] {
            match std::fs::symlink_metadata(
                self.metadata_root.join(name),
            ) {
                Ok(meta) if meta.is_file() && !meta.file_type().is_symlink() => {
                    used_bytes = used_bytes
                        .checked_add(meta.len())
                        .ok_or(Error::DownloadsUnavailable)?;
                }
                Ok(_) => return Err(Error::DownloadsUnavailable),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(_) => return Err(Error::DownloadsUnavailable),
            }
        }
        Ok(DownloadCapacity {
            total_bytes,
            free_bytes,
            used_bytes,
            other_downloads_bytes,
        })
    }
    /// Counts describe every persisted local row in this scope, independent of pagination.
    /// They never claim the server's full library/season/album size.
    pub fn groups(&self, viewer: &DownloadViewer) -> Result<Vec<DownloadGroup>> {
        let mut query=self.connection.prepare("SELECT CASE t.kind WHEN 'episode' THEN 'episodes' ELSE 'songs' END,t.parent_id,t.group_id,count(*) FROM download_entries e JOIN download_titles t ON t.entry_id=e.id WHERE e.scope_id=?1 AND t.kind IN ('episode','song','track') AND t.parent_id!='' GROUP BY 1,2,3 ORDER BY 1,2,3").map_err(unavailable)?;
        query
            .query_map([scope(viewer)?], |r| {
                Ok(DownloadGroup {
                    kind: r.get(0)?,
                    parent_id: r.get(1)?,
                    group_id: r.get(2)?,
                    count: count(r, 3)?,
                    revision: String::new(),
                    bytes: 0,
                    bytes_done: 0,
                    parent_title: None,
                    artist: None,
                    quality: None,
                    state: None,
                    held: None,
                    artwork_id: None,
                    offline_access: "needsCheckIn".into(),
                })
            })
            .map_err(unavailable)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(unavailable)
            .and_then(|mut groups| {
                for group in &mut groups {
                    group.revision = self.group_revision(
                        viewer,
                        &GroupSelector {
                            kind: group.kind.clone(),
                            parent_id: group.parent_id.clone(),
                            group_id: Some(group.group_id.clone()),
                        },
                    )?;
                    let selector = GroupSelector {
                        kind: group.kind.clone(),
                        parent_id: group.parent_id.clone(),
                        group_id: Some(group.group_id.clone()),
                    };
                    let members = self.group_members(viewer, &selector)?;
                    let first = members.first().ok_or(Error::DownloadsUnavailable)?;
                    group.parent_title=(!first.title.parent_title.trim().is_empty()&&members.iter().all(|m|m.title.parent_title==first.title.parent_title)).then(||first.title.parent_title.clone());
                    group.artist=(!first.title.artist.trim().is_empty()&&members.iter().all(|m|m.title.artist==first.title.artist)).then(||first.title.artist.clone());
                    group.bytes = members.iter().try_fold(0u64, |sum, m| {
                        sum.checked_add(m.bytes).ok_or(Error::DownloadsUnavailable)
                    })?;
                    group.bytes_done = members.iter().try_fold(0u64, |sum, m| {
                        sum.checked_add(m.bytes_done)
                            .ok_or(Error::DownloadsUnavailable)
                    })?;
                    group.quality = members
                        .iter()
                        .all(|m| m.quality == first.quality)
                        .then(|| first.quality.clone());
                    group.state = members
                        .iter()
                        .all(|m| m.state == first.state)
                        .then(|| first.state.clone());
                    group.held = members
                        .iter()
                        .all(|m| m.held == first.held)
                        .then_some(first.held);
                    group.artwork_id = members.iter().find_map(|m| {
                        self.connection
                            .query_row(
                                "SELECT entry_id FROM download_artwork WHERE entry_id=?1",
                                [&m.id],
                                |r| r.get(0),
                            )
                            .optional()
                            .ok()
                            .flatten()
                    });
                    if members
                        .iter()
                        .all(|m| self.playback_file(viewer, &m.id).is_ok())
                    {
                        group.offline_access = "valid".into();
                    }
                }
                Ok(groups)
            })
    }
    pub fn group_members(
        &self,
        viewer: &DownloadViewer,
        selector: &GroupSelector,
    ) -> Result<Vec<LocalDownload>> {
        selector.validate()?;
        let sql = READ_ENTRY.to_owned()
            + "WHERE e.scope_id=?1 AND t.parent_id=?2 AND (?3 IS NULL OR t.group_id=?3) AND ((?4='episodes' AND t.kind='episode') OR (?4='songs' AND t.kind IN ('song','track'))) ORDER BY t.group_number IS NULL,t.group_number,t.number IS NULL,t.number,e.id LIMIT 10001";
        let mut query = self.connection.prepare(&sql).map_err(unavailable)?;
        let members = query
            .query_map(
                params![
                    scope(viewer)?,
                    selector.parent_id,
                    selector.group_id,
                    selector.kind
                ],
                row_entry,
            )
            .map_err(unavailable)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(unavailable)?;
        if members.is_empty() || members.len() > 10000 {
            return Err(Error::DownloadsUnavailable);
        }
        Ok(members)
    }
    pub fn group_revision(
        &self,
        viewer: &DownloadViewer,
        selector: &GroupSelector,
    ) -> Result<String> {
        let members = self.group_members(viewer, selector)?;
        let mut context = Context::new(&SHA256);
        for member in members {
            context.update(member.id.as_bytes());
            context.update(&member.revision.to_be_bytes());
        }
        Ok(hex(context.finish().as_ref()))
    }
    pub fn act_group(
        &mut self,
        viewer: &DownloadViewer,
        selector: &GroupSelector,
        expected: &str,
        action: GroupAction,
    ) -> Result<()> {
        if !digest(expected) {
            return Err(Error::InvalidCommand);
        }
        self.connection
            .execute_batch("BEGIN IMMEDIATE")
            .map_err(unavailable)?;
        let mut retries = Vec::new();
        let result = (|| {
            if self.group_revision(viewer, selector)? != expected {
                return Err(Error::DownloadsConflict);
            }
            for entry in self.group_members(viewer, selector)? {
                match action {
                    GroupAction::Remove => {
                        self.connection
                            .execute(
                                "INSERT OR IGNORE INTO download_garbage VALUES(?1)",
                                [&entry.id],
                            )
                            .map_err(unavailable)?;
                        self.connection
                            .execute("DELETE FROM download_entries WHERE id=?1", [&entry.id])
                            .map_err(unavailable)?;
                    }
                    GroupAction::Pause | GroupAction::Resume if entry.state != "ready" => {
                        let held = matches!(action, GroupAction::Pause);
                        self.connection.execute("UPDATE download_entries SET held=?1,state=?2,revision=revision+1 WHERE id=?3",params![held,if held{"paused"}else{"queued"},entry.id]).map_err(unavailable)?;
                    }
                    GroupAction::Retry if entry.state == "failed" => {
                        let partial = self.path(&entry.id, false)?;
                        let backup = self.files.join(format!("{}.retry", entry.id));
                        if backup.exists() {
                            return Err(Error::DownloadsConflict);
                        }
                        open_file(&partial, false, false)?;
                        std::fs::rename(&partial, &backup).map_err(unavailable)?;
                        retries.push((partial, backup));
                        self.connection.execute("UPDATE download_entries SET bytes_done=0,held=0,state='queued',revision=revision+1 WHERE id=?1",[entry.id]).map_err(unavailable)?;
                    }
                    _ => {}
                }
            }
            self.connection.execute_batch("COMMIT").map_err(unavailable)
        })();
        if let Err(error) = result {
            let _ = self.connection.execute_batch("ROLLBACK");
            for (partial, backup) in retries {
                std::fs::rename(backup, partial).map_err(unavailable)?;
            }
            return Err(error);
        }
        for (_, backup) in retries {
            std::fs::remove_file(backup).map_err(unavailable)?;
        }
        self.collect_garbage()
    }
    pub fn playable_group(
        &self,
        viewer: &DownloadViewer,
        selector: &GroupSelector,
        expected: &str,
    ) -> Result<Vec<LocalDownload>> {
        if self.group_revision(viewer, selector)? != expected {
            return Err(Error::DownloadsConflict);
        }
        let members = self.group_members(viewer, selector)?;
        for member in &members {
            self.playback_file(viewer, &member.id)?;
        }
        Ok(members)
    }
    pub fn artwork(&self, viewer: &DownloadViewer, id: &str) -> Result<Option<DownloadArtwork>> {
        self.entry(viewer, id)?;
        self.connection
            .query_row(
                "SELECT mime,bytes FROM download_artwork WHERE entry_id=?1",
                [id],
                |r| {
                    Ok(DownloadArtwork {
                        mime: r.get(0)?,
                        bytes: r.get(1)?,
                    })
                },
            )
            .optional()
            .map_err(unavailable)
    }
    /// Image payloads arrive through the authenticated app adapter; IPC cannot request a
    /// native path or URL. SVG/HTML are never accepted as local artwork.
    pub fn save_artwork(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
        mime: &str,
        bytes: &[u8],
    ) -> Result<LocalDownload> {
        let magic = match mime {
            "image/png" => bytes.starts_with(b"\x89PNG\r\n\x1a\n"),
            "image/jpeg" => bytes.starts_with(b"\xff\xd8\xff"),
            "image/webp" => bytes.len() >= 12 && &bytes[..4] == b"RIFF" && &bytes[8..12] == b"WEBP",
            _ => false,
        };
        if !magic || bytes.len() > 4 * 1024 * 1024 {
            return Err(Error::InvalidCommand);
        }
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        if tx
            .execute(
                "UPDATE download_entries SET revision=revision+1 WHERE id=?1 AND revision=?2",
                params![id, expected_revision as i64],
            )
            .map_err(unavailable)?
            != 1
        {
            return Err(Error::DownloadsConflict);
        }
        tx.execute("INSERT INTO download_artwork VALUES(?1,?2,?3) ON CONFLICT(entry_id) DO UPDATE SET mime=excluded.mime,bytes=excluded.bytes",params![id,mime,bytes]).map_err(unavailable)?;
        tx.commit().map_err(unavailable)?;
        self.entry(viewer, id)
    }
    fn other_targets(&self, viewer: &DownloadViewer) -> Result<Vec<RemovalTarget>> {
        viewer.validate()?;
        let mut query=self.connection.prepare("SELECT e.id,e.revision,e.bytes_done+coalesce(length(a.bytes),0) FROM download_entries e JOIN download_scopes s ON s.id=e.scope_id LEFT JOIN download_artwork a ON a.entry_id=e.id WHERE s.authority=?1 AND s.server_id=?2 AND s.account_id=?3 AND s.profile_id!=?4 ORDER BY e.id").map_err(unavailable)?;
        query
            .query_map(
                params![
                    viewer.authority,
                    viewer.server_id,
                    viewer.account_id,
                    viewer.profile_id
                ],
                |r| {
                    Ok(RemovalTarget {
                        id: r.get(0)?,
                        revision: count(r, 1)?,
                        bytes: count(r, 2)?,
                    })
                },
            )
            .map_err(unavailable)?
            .collect::<rusqlite::Result<Vec<_>>>()
            .map_err(unavailable)
    }
    pub fn retire_confirmations(&mut self) {
        self.previews.clear();
    }
    pub fn preview_other_profiles(
        &mut self,
        viewer: &DownloadViewer,
    ) -> Result<OtherDownloadsPreview> {
        let targets = self.other_targets(viewer)?;
        let bytes = targets.iter().try_fold(0u64, |sum, t| {
            sum.checked_add(t.bytes).ok_or(Error::DownloadsUnavailable)
        })?;
        self.previews
            .retain(|_, v| v.created.elapsed() < Duration::from_secs(300));
        if self.previews.len() >= 16 {
            self.previews.clear()
        }
        let mut random = [0u8; 32];
        SystemRandom::new().fill(&mut random).map_err(unavailable)?;
        let snapshot_token = hex(&random);
        self.previews.insert(
            snapshot_token.clone(),
            RemovalSnapshot {
                viewer: viewer.clone(),
                targets,
                created: Instant::now(),
            },
        );
        Ok(OtherDownloadsPreview {
            snapshot_token,
            bytes,
        })
    }
    pub fn remove_other_profiles(&mut self, viewer: &DownloadViewer, token: &str) -> Result<()> {
        viewer.validate()?;
        let preview = self
            .previews
            .remove(token)
            .ok_or(Error::DownloadsConflict)?;
        if preview.viewer != *viewer || preview.created.elapsed() >= Duration::from_secs(300) {
            return Err(Error::DownloadsConflict);
        }
        // A transaction prevents a concurrent writer adding/changing targets between
        // the exact-set comparison and deletion. Any change rejects the entire action.
        self.connection
            .execute_batch("BEGIN IMMEDIATE")
            .map_err(unavailable)?;
        let result = (|| {
            if self.other_targets(viewer)? != preview.targets {
                return Err(Error::DownloadsConflict);
            }
            for target in preview.targets {
                self.connection
                    .execute(
                        "INSERT OR IGNORE INTO download_garbage VALUES(?1)",
                        [&target.id],
                    )
                    .map_err(unavailable)?;
                if self
                    .connection
                    .execute(
                        "DELETE FROM download_entries WHERE id=?1 AND revision=?2",
                        params![target.id, target.revision as i64],
                    )
                    .map_err(unavailable)?
                    != 1
                {
                    return Err(Error::DownloadsConflict);
                }
            }
            self.connection.execute_batch("COMMIT").map_err(unavailable)
        })();
        if let Err(error) = result {
            let _ = self.connection.execute_batch("ROLLBACK");
            return Err(error);
        }
        self.collect_garbage()
    }
    fn collect_garbage(&self) -> Result<()> {
        loop {
            let ids = {
                let mut query = self
                    .connection
                    .prepare("SELECT id FROM download_garbage LIMIT 200")
                    .map_err(unavailable)?;
                query
                    .query_map([], |r| r.get::<_, String>(0))
                    .map_err(unavailable)?
                    .collect::<rusqlite::Result<Vec<_>>>()
                    .map_err(unavailable)?
            };
            if ids.is_empty() {
                return Ok(());
            }
            for id in ids {
                for ready in [false, true] {
                    let path = self.path(&id, ready)?;
                    match std::fs::remove_file(path) {
                        Ok(()) => {}
                        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                        Err(_) => return Err(Error::DownloadsUnavailable),
                    }
                }
                match std::fs::remove_file(self.files.join(format!("{id}.retry"))) {
                    Ok(()) => {}
                    Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                    Err(_) => return Err(Error::DownloadsUnavailable),
                }
                self.connection
                    .execute("DELETE FROM download_garbage WHERE id=?1", [id])
                    .map_err(unavailable)?;
            }
        }
    }
    pub fn remove(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
    ) -> Result<()> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        tx.execute("INSERT OR IGNORE INTO download_garbage VALUES(?1)", [id])
            .map_err(unavailable)?;
        if tx
            .execute(
                "DELETE FROM download_entries WHERE id=?1 AND revision=?2",
                params![id, expected_revision as i64],
            )
            .map_err(unavailable)?
            != 1
        {
            return Err(Error::DownloadsConflict);
        }
        tx.commit().map_err(unavailable)?;
        self.collect_garbage()
    }
    /// Server deletion is a displayed fact. It never deletes a viewer's offline bytes.
    pub fn server_presence(
        &mut self,
        viewer: &DownloadViewer,
        id: &str,
        expected_revision: u64,
        on_server: bool,
    ) -> Result<LocalDownload> {
        let entry = self.entry(viewer, id)?;
        if entry.revision != expected_revision {
            return Err(Error::DownloadsConflict);
        }
        let tx = self
            .connection
            .transaction_with_behavior(TransactionBehavior::Immediate)
            .map_err(unavailable)?;
        if tx
            .execute(
                "UPDATE download_entries SET revision=revision+1 WHERE id=?1 AND revision=?2",
                params![id, expected_revision as i64],
            )
            .map_err(unavailable)?
            != 1
        {
            return Err(Error::DownloadsConflict);
        }
        tx.execute(
            "UPDATE download_titles SET on_server=?1 WHERE entry_id=?2",
            params![on_server, id],
        )
        .map_err(unavailable)?;
        tx.commit().map_err(unavailable)?;
        self.entry(viewer, id)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn rules() -> DownloadRules {
        DownloadRules {
            remove_watched: false,
            next_episode: false,
            unmetered_only: true,
            limit_bytes: 1024,
        }
    }
    fn root() -> PathBuf {
        std::env::temp_dir().join(format!(
            "desktop-download-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ))
    }
    fn viewer() -> DownloadViewer {
        DownloadViewer {
            authority: "local".into(),
            account_id: "account".into(),
            profile_id: "profile".into(),
            server_id: "server".into(),
        }
    }
    fn enrollment(bytes: &[u8]) -> Enrollment {
        Enrollment {
            viewer: viewer(),
            preparation_id: "preparation".into(),
            item_id: "item".into(),
            quality: "original".into(),
            sha256: hash(bytes),
            bytes: bytes.len() as u64,
            title: DownloadTitle {
                kind: "movie".into(),
                title: "A genuine title".into(),
                artist: String::new(),
                parent_id: String::new(),
                parent_title: String::new(),
                group_id: String::new(),
                group_title: String::new(),
                group_number: None,
                number: None,
                year: 2026,
                duration_ms: 10_000,
                on_server: true,
            },
        }
    }
    #[test]
    fn another_app_connection_cannot_race_the_file_journal() {
        let root=root();let mut first=DownloadStore::open(&root,&rules()).unwrap();
        let entry=first.enroll(enrollment(b"first")).unwrap();
        assert!(DownloadStore::open(&root,&rules()).is_err());
        let entry=first.append(&viewer(),&entry.id,entry.revision,0,b"first").unwrap();
        assert_eq!(entry.bytes_done,5);
        drop(first);let reopened=DownloadStore::open(&root,&rules()).unwrap();assert_eq!(reopened.entry(&viewer(),&entry.id).unwrap().bytes_done,5);
        drop(reopened);std::fs::remove_dir_all(root).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn folder_move_commits_files_and_pointer_together_without_changing_receipts_or_personal_files(){
        let root=root();let selected=root.join("selected");std::fs::create_dir_all(&selected).unwrap();
        std::fs::write(selected.join("personal.txt"),b"preserve").unwrap();
        let mut store=DownloadStore::open(&root,&rules()).unwrap();
        let entry=store.enroll(enrollment(b"hello")).unwrap();
        let entry=store.append(&viewer(),&entry.id,entry.revision,0,b"hello").unwrap();
        let ready=store.seal(&viewer(),&entry.id,entry.revision).unwrap();
        let (receipt,key)=crate::download_receipt::tests::signed(&viewer(),&ready.sha256,5);
        store.install_receipt_key(&viewer(),"key",&key).unwrap();store.save_receipt(&viewer(),&entry.id,receipt).unwrap();
        let original=store.playback_file(&viewer(),&entry.id).unwrap();
        std::fs::write(store.folder_path().join("unowned.txt"),b"retain here").unwrap();
        store.relocate_files(&selected,||true).unwrap();let moved=store.folder_path().to_path_buf();
        assert_eq!(std::fs::read(store.verified_playback(&viewer(),&entry.id).unwrap().path).unwrap(),b"hello");
        assert!(!original.exists());assert_eq!(std::fs::read(original.parent().unwrap().join("unowned.txt")).unwrap(),b"retain here");
        assert_eq!(std::fs::read(selected.join("personal.txt")).unwrap(),b"preserve");
        drop(store);let reopened=DownloadStore::open(&root,&rules()).unwrap();assert_eq!(reopened.folder_path(),moved);
        assert_eq!(std::fs::read(reopened.verified_playback(&viewer(),&entry.id).unwrap().path).unwrap(),b"hello");
        assert!(reopened.capacity(&viewer()).unwrap().used_bytes>5,"metadata stays counted in its original app-data directory");
        drop(reopened);std::fs::remove_dir_all(root).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn folder_copy_cancellation_and_pointer_commit_failure_retain_every_original(){
        use std::cell::Cell;
        let root=root();let selected=root.join("selected");std::fs::create_dir_all(&selected).unwrap();
        let mut store=DownloadStore::open(&root,&rules()).unwrap();
        let entry=store.enroll(enrollment(b"hello")).unwrap();store.append(&viewer(),&entry.id,entry.revision,0,b"hello").unwrap();
        let original=store.path(&entry.id,false).unwrap();let folder=store.folder_path().to_path_buf();
        let calls=Cell::new(0);assert!(store.relocate_files(&selected,||{calls.set(calls.get()+1);calls.get()<3}).is_err());
        assert_eq!(std::fs::read(&original).unwrap(),b"hello");assert_eq!(store.folder_path(),folder);
        assert_eq!(std::fs::read_dir(&selected).unwrap().count(),0);
        store.connection.execute_batch("CREATE TRIGGER reject_folder BEFORE UPDATE ON download_folder BEGIN SELECT RAISE(ABORT,'injected commit failure'); END;").unwrap();
        assert!(store.relocate_files(&selected,||true).is_err());
        assert_eq!(store.folder_path(),folder);assert_eq!(std::fs::read(&original).unwrap(),b"hello");assert_eq!(std::fs::read_dir(&selected).unwrap().count(),0);
        drop(store);let reopened=DownloadStore::open(&root,&rules()).unwrap();assert_eq!(reopened.folder_path(),folder);assert_eq!(std::fs::read(&original).unwrap(),b"hello");
        drop(reopened);std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn durable_chunks_recover_and_ready_requires_exact_hash_then_signed_viewer_receipt() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let partial = store.append(&viewer(), &entry.id, 0, 0, b"hel").unwrap();
        assert_eq!(partial.bytes_done, 3);
        drop(store);
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let resumed = store.entry(&viewer(), &entry.id).unwrap();
        let complete = store
            .append(&viewer(), &entry.id, resumed.revision, 3, b"lo")
            .unwrap();
        assert!(store.playback_file(&viewer(), &entry.id).is_err());
        let ready = store.seal(&viewer(), &entry.id, complete.revision).unwrap();
        assert_eq!(ready.state, "ready");
        assert_eq!(
            store.playback_file(&viewer(), &entry.id).unwrap_err(),
            Error::InvalidReceipt
        );
        let (receipt, key) = crate::download_receipt::tests::signed(&viewer(), &ready.sha256, 5);
        store.install_receipt_key(&viewer(), "key", &key).unwrap();
        store.save_receipt(&viewer(), &entry.id, receipt).unwrap();
        assert_eq!(
            std::fs::read(store.playback_file(&viewer(), &entry.id).unwrap()).unwrap(),
            b"hello"
        );
        let other = DownloadViewer {
            profile_id: "other".into(),
            ..viewer()
        };
        assert!(store.playback_file(&other, &entry.id).is_err());
        store.revoke(&viewer(), "receipt").unwrap();
        assert!(store.entry(&viewer(), &entry.id).is_err());
        assert!(!store.path(&entry.id,true).unwrap().exists());
        assert!(store.playback_file(&viewer(), &entry.id).is_err());
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn offsets_revisions_quota_and_hash_mismatch_never_acknowledge_ready() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        assert!(store.append(&viewer(), &entry.id, 0, 1, b"h").is_err());
        let partial = store.append(&viewer(), &entry.id, 0, 0, b"jello").unwrap();
        assert_eq!(
            store.append(&viewer(), &entry.id, 0, 0, b"h").unwrap_err(),
            Error::DownloadsConflict
        );
        assert_eq!(
            store
                .seal(&viewer(), &entry.id, partial.revision)
                .unwrap_err(),
            Error::DownloadVerificationFailed
        );
        assert_ne!(store.entry(&viewer(), &entry.id).unwrap().state, "ready");
        assert_eq!(
            store.enroll(enrollment(&[3; 2048])).unwrap_err(),
            Error::DownloadsConflict
        );
        let mut large = enrollment(&[3; 2048]);
        large.preparation_id = "large".into();
        assert_eq!(store.enroll(large).unwrap_err(), Error::StorageFull);
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn only_committed_bytes_survive_a_crash_and_user_pause_survives_reopen() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let partial = store.append(&viewer(), &entry.id, 0, 0, b"he").unwrap();
        store
            .hold(&viewer(), &entry.id, partial.revision, true)
            .unwrap();
        OpenOptions::new()
            .append(true)
            .open(store.path(&entry.id, false).unwrap())
            .unwrap()
            .write_all(b"uncommitted")
            .unwrap();
        drop(store);
        let store = DownloadStore::open(&root, &rules()).unwrap();
        let saved = store.entry(&viewer(), &entry.id).unwrap();
        assert_eq!(saved.state, "paused");
        assert!(saved.held);
        assert_eq!(saved.bytes_done, 2);
        assert_eq!(
            std::fs::read(store.path(&entry.id, false).unwrap()).unwrap(),
            b"he"
        );
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn deferred_progress_is_viewer_owned_and_a_newer_position_survives_an_old_ack() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let now = now_ms();
        store
            .note_play(
                &viewer(),
                &entry.id,
                PendingPlay {
                    play_id: "play".into(),
                    item_id: "item".into(),
                    position_ms: 1000,
                    ended: false,
                    observed_ms: now,
                },
            )
            .unwrap();
        store
            .note_play(
                &viewer(),
                &entry.id,
                PendingPlay {
                    play_id: "play".into(),
                    item_id: "item".into(),
                    position_ms: 2000,
                    ended: true,
                    observed_ms: now + 1,
                },
            )
            .unwrap();
        store.acknowledge_play(&viewer(), "play", now).unwrap();
        assert_eq!(store.pending_plays(&viewer()).unwrap()[0].position_ms, 2000);
        let other = DownloadViewer {
            account_id: "other".into(),
            ..viewer()
        };
        assert!(store.pending_plays(&other).unwrap().is_empty());
        store.acknowledge_play(&viewer(), "play", now + 1).unwrap();
        assert!(store.pending_plays(&viewer()).unwrap().is_empty());
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn native_rules_revisions_are_committed_and_reopening_never_resets_them() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let changed = DownloadRules {
            limit_bytes: 2000,
            ..rules()
        };
        assert_eq!(store.set_rules(0, changed.clone()).unwrap().revision, 1);
        assert_eq!(
            store.set_rules(0, rules()).unwrap_err(),
            Error::DownloadsConflict
        );
        drop(store);
        let store = DownloadStore::open(&root, &rules()).unwrap();
        assert_eq!(store.rules().unwrap().rules, changed);
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn remove_is_revision_fenced_and_server_absence_keeps_local_bytes() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let partial = store.append(&viewer(), &entry.id, 0, 0, b"hello").unwrap();
        let ready = store.seal(&viewer(), &entry.id, partial.revision).unwrap();
        let absent = store
            .server_presence(&viewer(), &entry.id, ready.revision, false)
            .unwrap();
        assert!(!absent.title.on_server);
        assert!(store.path(&entry.id, true).unwrap().exists());
        assert_eq!(
            store
                .remove(&viewer(), &entry.id, ready.revision)
                .unwrap_err(),
            Error::DownloadsConflict
        );
        let capacity = store.capacity(&viewer()).unwrap();
        assert!(capacity.total_bytes >= capacity.free_bytes);
        assert!(capacity.free_bytes > 0);
        assert!(capacity.used_bytes >= 5);
        assert_eq!(capacity.other_downloads_bytes, 0);
        store.remove(&viewer(), &entry.id, absent.revision).unwrap();
        assert!(store.list(&viewer(), None, 10).unwrap().items.is_empty());
        assert!(!store.path(&entry.id, true).unwrap().exists());
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn other_profile_confirmation_is_exact_scoped_and_rejects_any_changed_target() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let active = store.enroll(enrollment(b"active")).unwrap();
        let mut entries = Vec::new();
        for (field, value) in [
            ("profile", "other"),
            ("account", "other"),
            ("server", "other"),
            ("authority", "hosted"),
        ] {
            let mut input = enrollment(b"other");
            match field {
                "profile" => input.viewer.profile_id = value.into(),
                "account" => input.viewer.account_id = value.into(),
                "server" => input.viewer.server_id = value.into(),
                _ => input.viewer.authority = value.into(),
            }
            let owner = input.viewer.clone();
            let row = store.enroll(input).unwrap();
            let row = store
                .append(&owner, &row.id, row.revision, 0, b"other")
                .unwrap();
            entries.push((owner, row));
        }
        assert_eq!(store.capacity(&viewer()).unwrap().other_downloads_bytes, 5);
        let preview = store.preview_other_profiles(&viewer()).unwrap();
        assert_eq!(preview.bytes, 5);
        let (other, row) = &entries[0];
        store.hold(other, &row.id, row.revision, true).unwrap();
        assert_eq!(
            store.remove_other_profiles(&viewer(), &preview.snapshot_token),
            Err(Error::DownloadsConflict)
        );
        assert!(store.entry(other, &row.id).is_ok());
        let preview = store.preview_other_profiles(&viewer()).unwrap();
        let mut wrong = viewer();
        wrong.account_id = "other".into();
        assert_eq!(
            store.remove_other_profiles(&wrong, &preview.snapshot_token),
            Err(Error::DownloadsConflict)
        );
        let preview = store.preview_other_profiles(&viewer()).unwrap();
        store
            .remove_other_profiles(&viewer(), &preview.snapshot_token)
            .unwrap();
        assert!(store.entry(other, &row.id).is_err());
        assert!(store.entry(&viewer(), &active.id).is_ok());
        for (owner, row) in &entries[1..] {
            assert!(store.entry(owner, &row.id).is_ok());
        }
        assert_eq!(
            store.remove_other_profiles(&viewer(), &preview.snapshot_token),
            Err(Error::DownloadsConflict)
        );
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn native_groups_cover_all_pages_and_artwork_is_persisted_scoped_and_revision_fenced() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let mut ids = Vec::new();
        for i in 0..4 {
            let mut input = enrollment(b"media");
            input.preparation_id = format!("song{i}");
            input.item_id = format!("item{i}");
            input.title.kind = "song".into();
            input.title.parent_id = "album".into();
            input.title.group_id = "disc1".into();
            input.title.parent_title="Actual album".into();
            input.title.artist="Actual artist".into();
            ids.push(store.enroll(input).unwrap().id);
        }
        assert_eq!(store.list(&viewer(), None, 1).unwrap().items.len(), 1);
        let groups = store.groups(&viewer()).unwrap();
        assert_eq!(groups.len(), 1);
        let group = &groups[0];
        assert_eq!(
            (
                group.kind.as_str(),
                group.parent_id.as_str(),
                group.group_id.as_str(),
                group.count
            ),
            ("songs", "album", "disc1", 4)
        );
        assert_eq!((group.bytes, group.bytes_done), (20, 0));
        assert_eq!(group.quality.as_deref(), Some("original"));
        assert_eq!(group.parent_title.as_deref(),Some("Actual album"));
        assert_eq!(group.artist.as_deref(),Some("Actual artist"));
        assert_eq!(group.offline_access, "needsCheckIn");
        assert_eq!(
            group.revision,
            store
                .group_revision(
                    &viewer(),
                    &GroupSelector {
                        kind: "songs".into(),
                        parent_id: "album".into(),
                        group_id: Some("disc1".into())
                    }
                )
                .unwrap()
        );
        let mut guest=enrollment(b"media");guest.preparation_id="guest".into();guest.item_id="guest".into();guest.title.kind="song".into();guest.title.parent_id="album".into();guest.title.group_id="disc1".into();guest.title.parent_title="Actual album".into();guest.title.artist="Guest artist".into();
        store.enroll(guest).unwrap();
        let mixed=store.groups(&viewer()).unwrap();assert_eq!(mixed[0].count,5);assert_eq!(mixed[0].parent_title.as_deref(),Some("Actual album"));assert!(mixed[0].artist.is_none());
        let mut different=enrollment(b"media");different.preparation_id="different".into();different.item_id="different".into();different.title.kind="song".into();different.title.parent_id="album".into();different.title.group_id="disc1".into();different.title.parent_title="Different actual album title".into();different.title.artist="Actual artist".into();store.enroll(different).unwrap();
        let mixed=store.groups(&viewer()).unwrap();assert_eq!(mixed[0].count,6);assert!(mixed[0].parent_title.is_none());assert!(mixed[0].artist.is_none());
        assert!(!serde_json::to_string(&mixed[0]).unwrap().contains("parentTitle"));
        assert!(!serde_json::to_string(&mixed[0]).unwrap().contains("artist"));
        assert!(store.artwork(&viewer(), &ids[0]).unwrap().is_none());
        assert_eq!(
            store
                .save_artwork(&viewer(), &ids[0], 0, "image/svg+xml", b"<svg/> ")
                .unwrap_err(),
            Error::InvalidCommand
        );
        let payload = b"\x89PNG\r\n\x1a\nactual stored image payload";
        let saved = store
            .save_artwork(&viewer(), &ids[0], 0, "image/png", payload)
            .unwrap();
        assert_eq!(saved.revision, 1);
        assert_eq!(
            store
                .save_artwork(&viewer(), &ids[0], 0, "image/png", payload)
                .unwrap_err(),
            Error::DownloadsConflict
        );
        let mut other = viewer();
        other.profile_id = "other".into();
        assert!(store.artwork(&other, &ids[0]).is_err());
        drop(store);
        let store = DownloadStore::open(&root, &rules()).unwrap();
        let image = store.artwork(&viewer(), &ids[0]).unwrap().unwrap();
        assert_eq!(image.mime, "image/png");
        assert_eq!(image.bytes, payload);
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn group_retry_and_remove_are_complete_revision_fenced_transactions() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let mut ids = Vec::new();
        for i in 0..2 {
            let mut input = enrollment(b"hello");
            input.preparation_id = format!("prep{i}");
            input.item_id = format!("track{i}");
            input.title.kind = "song".into();
            input.title.parent_id = "album".into();
            input.title.group_number = Some(1);
            input.title.number = Some(2 - i);
            let row = store.enroll(input).unwrap();
            let row = store
                .append(&viewer(), &row.id, row.revision, 0, b"wrong")
                .unwrap();
            assert_eq!(
                store.seal(&viewer(), &row.id, row.revision).unwrap_err(),
                Error::DownloadVerificationFailed
            );
            ids.push(row.id);
        }
        let selector = GroupSelector {
            kind: "songs".into(),
            parent_id: "album".into(),
            group_id: None,
        };
        let before = store.group_revision(&viewer(), &selector).unwrap();
        let members = store.group_members(&viewer(), &selector).unwrap();
        assert_eq!(members[0].id, ids[1]);
        store.connection.execute_batch("CREATE TRIGGER fail_retry BEFORE UPDATE ON download_entries WHEN NEW.bytes_done=0 BEGIN SELECT RAISE(ABORT,'fixture'); END;").unwrap();
        assert!(
            store
                .act_group(&viewer(), &selector, &before, GroupAction::Retry)
                .is_err()
        );
        for id in &ids {
            assert_eq!(
                std::fs::read(store.path(id, false).unwrap()).unwrap(),
                b"wrong"
            );
            assert_eq!(store.entry(&viewer(), id).unwrap().state, "failed");
        }
        store
            .connection
            .execute_batch("DROP TRIGGER fail_retry;")
            .unwrap();
        store
            .act_group(&viewer(), &selector, &before, GroupAction::Retry)
            .unwrap();
        for id in &ids {
            let row = store.entry(&viewer(), id).unwrap();
            assert_eq!((row.bytes_done, row.state.as_str()), (0, "queued"));
        }
        assert_eq!(
            store.act_group(&viewer(), &selector, &before, GroupAction::Remove),
            Err(Error::DownloadsConflict)
        );
        let current = store.group_revision(&viewer(), &selector).unwrap();
        store
            .act_group(&viewer(), &selector, &current, GroupAction::Remove)
            .unwrap();
        assert!(store.list(&viewer(), None, 100).unwrap().items.is_empty());
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn failed_ready_commit_restores_partial_file_without_claiming_ready() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let partial = store.append(&viewer(), &entry.id, 0, 0, b"hello").unwrap();
        store.connection.execute_batch("CREATE TRIGGER reject_ready BEFORE UPDATE ON download_entries WHEN NEW.state='ready' BEGIN SELECT RAISE(ABORT,'reject'); END;").unwrap();
        assert_eq!(
            store
                .seal(&viewer(), &entry.id, partial.revision)
                .unwrap_err(),
            Error::DownloadsUnavailable
        );
        assert!(store.path(&entry.id, false).unwrap().exists());
        assert!(!store.path(&entry.id, true).unwrap().exists());
        assert_eq!(
            store.entry(&viewer(), &entry.id).unwrap().state,
            "downloading"
        );
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn native_generated_file_names_never_follow_a_symlink_into_other_files() {
        let root = root();
        let mut store = DownloadStore::open(&root, &rules()).unwrap();
        let entry = store.enroll(enrollment(b"hello")).unwrap();
        let original = root.join("original");
        std::fs::write(&original, b"preserve").unwrap();
        std::os::unix::fs::symlink(&original, store.path(&entry.id, false).unwrap()).unwrap();
        assert!(store.append(&viewer(), &entry.id, 0, 0, b"hello").is_err());
        assert_eq!(std::fs::read(&original).unwrap(), b"preserve");
        store.remove(&viewer(), &entry.id, 0).unwrap();
        assert_eq!(std::fs::read(&original).unwrap(), b"preserve");
        drop(store);
        std::fs::remove_dir_all(root).unwrap();
    }
}
