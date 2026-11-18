' Finding Portico servers on this network (Use a local server): a server that has "Find this server
' at home" on answers multicast DNS for _portico._tcp.local (server-next/internal/discovery) with
' its address, port and a TXT record "v=1", "id=<server id>", "fp=<fingerprint>", never its name.
' An answer is only a hint: the app asks the server itself (/v1/system) and goes on only when it
' names the same id.

' The question: one PTR question for _portico._tcp.local, as bytes to send.
function mdnsQuery() as object
    bytes = createObject("roByteArray")
    bytes.fromHexString("000000000001000000000000" + mdnsName() + "000C0001")
    return bytes
end function

function mdnsName() as string
    hex = ""
    for each label in ["_portico", "_tcp", "local"]
        part = createObject("roByteArray")
        part.fromAsciiString(label)
        hex = hex + right("0" + stri(len(label), 16).trim(), 2) + part.toHexString()
    end for
    return hex + "00"
end function

' A server's answer read: {id, fp, ip, port}, or invalid when it is not one (a question, a
' goodbye with time to live 0, another service, or bytes that run short).
function mdnsServer(bytes as object, length as integer) as dynamic
    if length < 12 or (bytes[2] and &h80) = 0 then return invalid      ' a question, not an answer
    count = bytes[6] * 256 + bytes[7] + bytes[8] * 256 + bytes[9] + bytes[10] * 256 + bytes[11]
    at = 12
    for q = 1 to bytes[4] * 256 + bytes[5]      ' questions an answer repeats: skipped
        at = mdnsSkipName(bytes, at, length) + 4
    end for
    found = {id: "", fp: "", ip: "", port: 0, v: ""}
    for r = 1 to count
        at = mdnsSkipName(bytes, at, length)
        if at < 0 or at + 10 > length then return invalid
        kind = bytes[at] * 256 + bytes[at + 1]
        live = bytes[at + 4] * 16777216 + bytes[at + 5] * 65536 + bytes[at + 6] * 256 + bytes[at + 7]
        size = bytes[at + 8] * 256 + bytes[at + 9]
        at = at + 10
        if at + size > length then return invalid
        if kind = 16
            if live = 0 then return invalid      ' the server said goodbye
            mdnsText(bytes, at, size, found)
        else if kind = 33 and size >= 6
            found.port = bytes[at + 4] * 256 + bytes[at + 5]
        else if kind = 1 and size = 4
            found.ip = bytes[at].toStr() + "." + bytes[at + 1].toStr() + "." + bytes[at + 2].toStr() + "." + bytes[at + 3].toStr()
        end if
        at = at + size
    end for
    if found.v <> "1" or left(found.id, 4) <> "srv_" or found.ip = "" or found.port = 0 then return invalid
    return {id: found.id, fp: found.fp, ip: found.ip, port: found.port}
end function

' Past a name: its labels to the end, or to a pointer (two bytes) into an earlier one. -1 if it runs short.
function mdnsSkipName(bytes as object, at as integer, length as integer) as integer
    while at >= 0 and at < length
        n = bytes[at]
        if n = 0 then return at + 1
        if (n and &hC0) = &hC0 then return at + 2
        at = at + 1 + n
    end while
    return -1
end function

' The TXT record's strings, each "key=value": v, id and fp are kept.
sub mdnsText(bytes as object, at as integer, size as integer, found as object)
    stop_ = at + size
    while at < stop_
        n = bytes[at]
        text = ""
        for i = at + 1 to at + n
            if i < stop_ then text = text + chr(bytes[i])
        end for
        eq = instr(1, text, "=")
        if eq > 0
            key = left(text, eq - 1)
            if key = "v" or key = "id" or key = "fp" then found[key] = mid(text, eq + 1)
        end if
        at = at + 1 + n
    end while
end sub
