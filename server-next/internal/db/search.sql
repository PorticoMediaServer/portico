-- Portico search index (search.db).
--
-- This file is thrown away and rebuilt from main.db whenever it is missing, unreadable, or was
-- built by different rules. Nothing in it is an original: see search_index.go.
--
-- The text columns hold words already folded by internal/searchtext, separated by spaces, so
-- the plain "ascii" tokeniser only has to split on those spaces. The prefix indexes make the
-- first letters a person types cheap.

-- format: what build of this schema wrote the file. folding: the word rules and Unicode tables
-- its text was folded with. cursor: the last row of main.db's changes table the index reflects.
CREATE TABLE meta (
  key   TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- One row per searchable item or person. The rowid is the result group's code shifted left
-- 40 bits, plus the item's or the person's id, so one kind of result is a range of rowids and
-- every kind is counted in one pass.
--   title   the title's or the name's words
--   joined  the same with words that punctuation breaks kept whole ("spiderman"); empty when no different
--   other   an item's original title, and the titles it sits under (its show, artist or album)
--   keys    words no person can type: the whole title as one word, to find a title that is exactly
--           what was typed; the id of each item it sits under, to find everything under a renamed
--           or deleted title; and an item's library, its rating age and the tags it and what it sits
--           under carry, to narrow a count
CREATE VIRTUAL TABLE search_titles USING fts5(title, joined, other, keys, tokenize = 'ascii', prefix = '1 2 3');
