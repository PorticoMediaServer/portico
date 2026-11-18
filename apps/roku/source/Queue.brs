' Play queues (the server's /v1/queues, as the other Portico apps use them). Every play is a queue:
' an episode's is its show, from that episode on, so the next one is known; a song's is its album
' from that song; a film's holds the film.
' These read what the server answers; the player makes the requests.

' What POST /v1/queues is asked to build for a title.
function queueBody(item as object) as object
    if item.kind = "episode" and item.rootId <> invalid then return {source: {container: item.rootId}, startAt: item.id}
    ' a song plays on through its album
    if item.kind = "track" and item.parentId <> invalid then return {source: {container: item.parentId}, startAt: item.id}
    return {source: {items: [item.id]}}
end function

' The entry with this id in a run of entries, or invalid.
function queueEntry(window as dynamic, entryId as dynamic) as dynamic
    if window = invalid or entryId = invalid then return invalid
    for each entry in window
        if entry.entryId = entryId and entry.available = true then return entry
    end for
    return invalid
end function

' A queue entry as the title the player plays.
function queueItem(entry as object) as object
    return {id: entry.itemId, kind: entry.kind, title: entry.title, rootId: entry.rootId, rootTitle: entry.rootTitle, parentIndex: entry.parentIndex, index: entry.index, durationMs: entry.durationMs, images: entry.images, year: entry.year}
end function
