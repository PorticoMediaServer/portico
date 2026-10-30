# Accepted filename issue dates

This additive private fact supports Root20 periodical grouping/date producers. It does not classify a publication, infer cadence, create a series, retype a library, or expose a new owner editor field.

An accepted inventory filename may supply exactly one delimited ISO `YYYY-MM` or `YYYY-MM-DD` date. The original precision is retained; a month never becomes an invented day. Calendar validation uses the exact Gregorian month/day, including leap years; year zero, invalid days, multiple dates, year-only values, localized/ambiguous numeric dates and date fragments adjoining letters, numbers or hyphens remain absent. Only the basename without its extension is examined, with a 4096-byte bound. Directory dates do not supply issue dates.

The existing real inventory job's library/root/generation/incarnation/path fence governs publication in the same transaction. The stored accepted file-name value retains its actual inventory row/revision; the canonical primary/current-root automatic resolver is unchanged. A successful scan without a date removes old filename date evidence. Unchanged evidence retains the existing sorted no-op comparison. Owner attribution rows remain untouched.

The same immutable ready attribution members carry the accepted date and its file-name source alongside type/series, with a library+generation+type+series+date seek index. Date source changes invalidate that ready generation through the existing source-value triggers. This is only date evidence: periodical membership still needs an accepted publication type and the other grouping facts, and newspaper calendar entries require actual day precision. No pivot, group, issue row or full-library scan completion is claimed by this prerequisite.

Official persistence metadata generation belongs to the Release checkpoint batch.
