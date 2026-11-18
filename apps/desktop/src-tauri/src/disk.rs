//! The room on the disk that holds a folder, for downloads that must fit (downloads rule 23).

/// Free bytes this account may use on the disk holding `path`, or None if the system won't say.
#[cfg(unix)]
pub fn free(path: &std::path::Path) -> Option<u64> {
    use std::os::unix::ffi::OsStrExt;
    let path = std::ffi::CString::new(path.as_os_str().as_bytes()).ok()?;
    // SAFETY: `path` is a NUL-terminated string and `stats` a zeroed struct statvfs fills in.
    let mut stats: libc::statvfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statvfs(path.as_ptr(), &mut stats) } != 0 { return None; }
    Some((stats.f_bavail as u64).saturating_mul(stats.f_frsize as u64))
}

/// The size of the disk holding `path`, or None if the system won't say.
#[cfg(unix)]
pub fn total(path: &std::path::Path) -> Option<u64> {
    use std::os::unix::ffi::OsStrExt;
    let path = std::ffi::CString::new(path.as_os_str().as_bytes()).ok()?;
    // SAFETY: as in `free`.
    let mut stats: libc::statvfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statvfs(path.as_ptr(), &mut stats) } != 0 { return None; }
    Some((stats.f_blocks as u64).saturating_mul(stats.f_frsize as u64))
}

#[cfg(windows)]
pub fn free(path: &std::path::Path) -> Option<u64> {
    use std::os::windows::ffi::OsStrExt;
    let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
    let mut free = 0u64;
    // SAFETY: `wide` is NUL-terminated; the two totals the app doesn't need may be null.
    let ok = unsafe { windows_sys::Win32::Storage::FileSystem::GetDiskFreeSpaceExW(wide.as_ptr(), &mut free, std::ptr::null_mut(), std::ptr::null_mut()) };
    (ok != 0).then_some(free)
}

#[cfg(windows)]
pub fn total(path: &std::path::Path) -> Option<u64> {
    use std::os::windows::ffi::OsStrExt;
    let wide: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
    let mut total = 0u64;
    // SAFETY: as in `free`.
    let ok = unsafe { windows_sys::Win32::Storage::FileSystem::GetDiskFreeSpaceExW(wide.as_ptr(), std::ptr::null_mut(), &mut total, std::ptr::null_mut()) };
    (ok != 0).then_some(total)
}

#[cfg(test)]
mod tests {
    #[test]
    fn a_real_folder_has_some_room_and_a_missing_one_says_nothing() {
        let (free, total) = (super::free(&std::env::temp_dir()).unwrap(), super::total(&std::env::temp_dir()).unwrap());
        assert!(free > 0 && total >= free, "{free} of {total}");
        assert_eq!(super::free(std::path::Path::new("/no/such/folder/for/portico")), None);
    }
}
