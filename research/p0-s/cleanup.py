import pathlib,sys
p=pathlib.Path(sys.argv[1])
for name in ['library.db','library.db-wal','library.db-shm','chronology.db','chronology.db-wal','chronology.db-shm']:
 (p/name).unlink(missing_ok=True)
