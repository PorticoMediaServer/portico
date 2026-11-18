' The servers this TV is signed in to, for Switch server (connect.html frame 24), and the account's
' server list from Portico Services. Kept in the store as "servers": a list of
' {origin, serverId, name, kind ("local" or "portico"), refreshToken, profileId}. The server in use
' is the store's "origin"; its own sign-in stays in the store's refreshToken and profileId, which the
' renewals keep current, and is copied into its entry only when the TV switches away from it.

' Portico Services, where a Portico Account lives (the native apps' hostedUrl).
function services() as string
    return "https://web.getportico.tv"
end function

function rememberedServers(text as string) as object
    if text = "" then return []
    list = parseJson(text)
    if type(list) <> "roArray" then return []
    return list
end function

' The list with this server in it: its entry replaced (the fields given win), or added at the end.
function withServer(list as object, entry as object) as object
    out = []
    found = false
    for each server in list
        if server.origin = entry.origin
            merged = {}
            merged.append(server)
            merged.append(entry)
            out.push(merged)
            found = true
        else
            out.push(server)
        end if
    end for
    if not found then out.push(entry)
    return out
end function

function withoutServer(list as object, origin as string) as object
    out = []
    for each server in list
        if server.origin <> origin then out.push(server)
    end for
    return out
end function

function serverAt(list as object, origin as string) as dynamic
    for each server in list
        if server.origin = origin then return server
    end for
    return invalid
end function

' The servers of Services' list (GET /v1/servers): its signed payload is base64url JSON.
function listedServers(payload as dynamic) as object
    if payload = invalid then return []
    text = payload.replace("-", "+").replace("_", "/")
    while len(text) mod 4 <> 0
        text = text + "="
    end while
    bytes = createObject("roByteArray")
    bytes.fromBase64String(text)
    list = parseJson(bytes.toAsciiString())
    if list = invalid or list.servers = invalid then return []
    return list.servers
end function

' --- Portico Services' list, checked a second time on a Roku (owner ruling 16; _scratch/roku/ACCOUNT-SIGNATURE.md) ---

' The keys this app carries for the list's RSA signature (source/services-list-<keyId>.pem).
function listKeyIds() as object
    return ["eSciPMCuwQAE"]
end function

' What is wrong with a list whose signature holds, or "" when nothing is: client-core's openServerList
' rules (packages/client-core/src/portico-server-list.ts): the signed-in account's list, a whole
' version of 1 or more and never lower than the one this TV holds for that account, and each server
' with a name, its addresses as text and an id that is the digest of its key. idOf turns a server's
' public key (base64url) into the id it must have, or "" for a key that is not 32 bytes.
function listRules(list as dynamic, accountId as string, held as dynamic, idOf as function) as string
    if type(list) <> "roAssociativeArray" or list.kind <> "portico.server-list" or type(list.servers) <> "roArray" then return "unreadable"
    if accountId = "" or not isText(list.accountId) or list.accountId <> accountId then return "account"
    v = list.version
    if not (type(v) = "roInt" or type(v) = "Integer" or type(v) = "LongInteger" or type(v) = "roLongInteger") or v < 1 then return "unreadable"
    if held <> invalid and v < held then return "older"
    for each server in list.servers
        if type(server) <> "roAssociativeArray" or not isText(server.name) or not isText(server.serverId) or not isText(server.publicKey) then return "unreadable"
        if type(server.addresses) <> "roArray" then return "unreadable"
        for each address in server.addresses
            if not isText(address) then return "unreadable"
        end for
        if server.serverId <> idOf(server.publicKey) then return "id"
    end for
    return ""
end function

function isText(value as dynamic) as boolean
    return type(value) = "roString" or type(value) = "String"
end function

' base64url to bytes.
function base64urlBytes(text as string) as object
    standard = text.replace("-", "+").replace("_", "/")
    while len(standard) mod 4 <> 0
        standard = standard + "="
    end while
    bytes = createObject("roByteArray")
    bytes.fromBase64String(standard)
    return bytes
end function

function bytesBase64url(bytes as object) as string
    return bytes.toBase64String().replace("+", "-").replace("/", "_").replace("=", "")
end function

' A server found on the home network: when this TV's signed account list knows its id, the
' fingerprint it announces (TXT fp, base64url SHA-256 of its key) must be the one pinned from that
' list (owner ruling 18). A server the list does not know is checked by its id alone.
function printHolds(prints as dynamic, record as object) as boolean
    if type(prints) <> "roAssociativeArray" or prints[record.id] = invalid then return true
    return record.fp = prints[record.id]
end function
