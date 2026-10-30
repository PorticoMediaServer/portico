Recorded server responses (O-159), one directory per release, read by `test/compat-corpus.test.ts`, which runs the current client parsers over them.
Written by `scripts/compat-corpus/record.mjs` against a throwaway server holding a synthetic music library: no real media, accounts or users.
Scrubbed at record time (tokens, keys, signatures, nonces, passwords and `/home/` paths become `<scrubbed>` or `/media/…`); a CI test fails on anything credential-shaped.
`648` is a pre-1.0 stand-in that only proves the mechanism; `main` was recorded from the tree that added it. Add a directory per release, never edit a recorded file by hand.
